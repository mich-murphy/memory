package capturesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// ClassificationError is a content-free coverage gap with private source coordinates.
// Unsupported hints are not raw-capture requests. Retain the Issue for coverage.
type ClassificationError struct{ Issue Issue }

func (e *ClassificationError) Error() string { return "capturesource: " + e.Issue.Code }

// Classify admits one absolute path under the most specific configured agent root.
// It performs bounded metadata/policy checks, never reads bodies or scans siblings.
// The caller must recheck policy and safely open the source during capture.
func Classify(ctx context.Context, opts Options, agent, path string) (sessioncapture.Request, error) {
	return classifyPath(ctx, opts, agent, path, false)
}

func classifyPath(ctx context.Context, opts Options, agent, path string, native bool) (sessioncapture.Request, error) {
	var zero sessioncapture.Request
	if err := defaults(&opts); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if opts.Coordinates != nil {
		return classifyCoordinate(ctx, opts, agent, path, native)
	}
	return classifyLegacyPath(ctx, opts, agent, path, native)
}

func classifyLegacyPath(ctx context.Context, opts Options, agent, path string, native bool) (sessioncapture.Request, error) {
	var zero sessioncapture.Request
	roots, err := rootsFor(opts)
	if err != nil {
		return zero, err
	}
	root := Root{Agent: agent}
	fail := func(code string) (sessioncapture.Request, error) {
		return zero, &ClassificationError{Issue{root, path, code}}
	}
	if len(strings.Split(filepath.Clean(path), string(filepath.Separator))) > 64 {
		return fail("depth_budget")
	}
	if !filepath.IsAbs(path) || strings.Contains(path, "\x00") {
		return fail("absolute_path_required")
	}
	path = filepath.Clean(path)
	root = owningRoot(roots, agent, path)
	if root.Path == "" {
		if standalonePrimary(opts, agent, path) {
			return classifyPrimary(ctx, opts)
		}
		return fail("outside_inventory")
	}
	policy, err := capturepolicy.New(opts.ConfigPath, opts.StateDir)
	if err != nil || opts.Policy == nil {
		return fail("policy_unavailable")
	}
	if !allowed(ctx, opts, policy, root, root.Path) || !allowed(ctx, opts, policy, root, path) {
		return fail("policy_denied")
	}
	info, code := pathInfo(ctx, opts, policy, root, path)
	if nativeMissing(native, code) {
		return classifyAbsentNative(ctx, opts, policy, root, path)
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if code != "" {
		return fail(code)
	}
	if !info.Mode().IsRegular() {
		return fail("nonregular_source")
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	q, code := classifyInfo(ctx, opts, policy, root, adapters.All(opts.Home)[agent], path, info, now().UnixNano())
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if code != "" {
		return fail(code)
	}
	return q, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func classifyInfo(ctx context.Context, opts Options, policy *capturepolicy.Policy, root Root, a adapters.Adapter, path string, info os.FileInfo, observed int64) (sessioncapture.Request, string) {
	// Never call Claude's legacy external-artifact resolver (it scans projects).
	rel, _ := filepath.Rel(root.Path, path)
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if root.Agent == "claude" && (first == "file-history" || first == "session-env" || first == "debug" || first == "todos" || first == "tasks") {
		return classifyExternal(ctx, opts, policy, root, a, path, info, observed)
	}
	sf, transform, code := classifySource(opts.Derived, root, a, path)
	if code != "" {
		return sessioncapture.Request{}, code
	}
	if info.Mode()&0o111 != 0 {
		return sessioncapture.Request{}, "executable_source_requires_review"
	}
	// Recheck after metadata/classification; the queue must repeat identity-aware
	// checks when opening the source (this discovery is not a TOCTOU guarantee).
	if !allowed(ctx, opts, policy, root, path) {
		return sessioncapture.Request{}, "policy_denied"
	}
	return emitRequest(opts, root, sf, path, observed, transform), ""
}

func emitRequest(opts Options, root Root, sf adapters.SessionFile, path string, observed int64, transform string) sessioncapture.Request {
	role := "artifact"
	switch sf.Role {
	case adapters.RoleMain:
		role = "main"
	case adapters.RoleSubagent:
		role = "subagent"
	case adapters.RoleToolResult:
		role = "tool-result"
	case adapters.RoleSuperseded, adapters.RoleAux, adapters.RoleFileHistory, adapters.RoleSessionEnv, adapters.RoleDebug, adapters.RoleTodo, adapters.RoleTask, adapters.RoleTeam, adapters.RolePlan, adapters.RolePaste, adapters.RoleUsage, adapters.RoleMemory, adapters.RoleHistory, adapters.RoleLiveness, adapters.RoleExperimental, adapters.RoleConfig, adapters.RoleLog, adapters.RoleDB, adapters.RoleUnknown:
	}
	sum := sha256.Sum256([]byte(root.Path))
	namespace := hex.EncodeToString(sum[:])
	session := "session/" + namespace + "/" + sf.SessionKey
	association := "ownership:session"
	if sf.SessionKey == "" {
		session = "resource/" + namespace + "/" + sf.Key
		association = "ownership:agent-resource"
	}
	q := sessioncapture.Request{Identity: publication.Identity{ArchiveID: opts.ArchiveID, MachineID: opts.MachineID, Agent: root.Agent, SessionID: session}, SourceID: sf.Key, Role: role, Path: path, Optional: role == "artifact" && sf.SessionKey != "", ObservedAtNS: observed, Associations: []string{association, "adapter-role:" + string(sf.Role), "root:" + root.Path}, Transform: transform}
	if sf.SessionKey != "" {
		q.Associations = append(q.Associations, "adapter-session:"+sf.SessionKey)
	}
	if root.Agent == "claude" && sf.Role == adapters.RoleMain {
		if uuid := sessioncapture.OwnerUUID(path); uuid != "" {
			q.Associations = append(q.Associations, "owner-uuid:"+uuid)
		}
	}
	if transform != "" {
		q.Associations = append(q.Associations, "transform:"+transform)
	}
	return q
}

func pathInfo(ctx context.Context, opts Options, policy *capturepolicy.Policy, root Root, path string) (os.FileInfo, string) {
	// Lstat every ancestor, including the configured root: aliases must never turn
	// a hook into access outside the bounded inventory. No directory is enumerated.
	rel, _ := filepath.Rel(root.Path, path)
	current := root.Path
	parts := strings.Split(rel, string(filepath.Separator))
	info, err := os.Lstat(current)
	if err != nil {
		return nil, "source_unreadable"
	}
	if !info.IsDir() {
		return nil, "root_not_directory_or_alias"
	}
	for i, part := range parts {
		if err := ctx.Err(); err != nil {
			return nil, "canceled"
		}
		current = filepath.Join(current, part)
		if !allowed(ctx, opts, policy, root, current) {
			return nil, "policy_denied"
		}
		info, err = os.Lstat(current)
		if err != nil {
			return nil, "source_unreadable"
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, "alias_integration_required"
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, "nonregular_source"
		}
	}

	return info, ""
}

// owningRoot is shared so hook hints and overlapping periodic inventories have
// exactly one namespace, independent of configured root order.
func owningRoot(roots []Root, agent, path string) Root {
	root := Root{Agent: agent}
	for _, candidate := range roots {
		if candidate.Agent == agent && within(candidate.Path, path) && len(candidate.Path) > len(root.Path) {
			root = candidate
		}
	}
	return root
}
