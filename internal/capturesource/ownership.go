package capturesource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

var externalUUIDPrefix = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func externalRole(top string) adapters.Role {
	switch top {
	case "file-history":
		return adapters.RoleFileHistory
	case "session-env":
		return adapters.RoleSessionEnv
	case "debug":
		return adapters.RoleDebug
	case "todos":
		return adapters.RoleTodo
	case "tasks":
		return adapters.RoleTask
	default:
		return adapters.RoleUnknown
	}
}

// OwnerMainPath validates an indexed ownership tuple against the bounded adapter
// layout. It does not authorize reads; callers must still check both coordinates.
func OwnerMainPath(a adapters.Adapter, root Root, uuid, session, legacy string) (string, bool) {
	if !sessioncapture.OwnershipSession(root.Path, session, legacy) {
		return "", false
	}
	prefix := strings.TrimSuffix(derivedKey(a, root.Path, "ownership-marker"), "ownership-marker")
	local, ok := strings.CutPrefix(legacy, prefix)
	if !ok {
		return "", false
	}
	parts := strings.Split(local, "/")
	if len(parts) != 2 || parts[0] == "" || parts[0] == "." || parts[0] == ".." || parts[1] != uuid || strings.ContainsAny(local, "\\\x00") {
		return "", false
	}
	return filepath.Join(root.Path, "projects", parts[0], uuid+".jsonl"), true
}

func classifyExternal(ctx context.Context, opts Options, policy *capturepolicy.Policy, root Root, a adapters.Adapter, path string, info os.FileInfo, observed int64) (sessioncapture.Request, string) {
	if opts.LookupOwner == nil {
		return sessioncapture.Request{}, "ownership_integration_required"
	}
	parts := strings.Split(relativeKey(root.Path, path), "/")
	uuid := ""
	if len(parts) > 1 {
		uuid = externalUUIDPrefix.FindString(parts[1])
	}
	if uuid == "" {
		return sessioncapture.Request{}, "ownership_pending"
	}
	session, legacy, err := opts.LookupOwner(ctx, root, uuid)
	if errors.Is(err, sessioncapture.ErrOwnershipAmbiguous) {
		return sessioncapture.Request{}, "ownership_ambiguous"
	}
	if err != nil {
		return sessioncapture.Request{}, "ownership_pending"
	}
	main, ok := OwnerMainPath(a, root, uuid, session, legacy)
	if !ok {
		return sessioncapture.Request{}, "ownership_pending"
	}
	// Ownership does not authorize bytes. Recheck policy at the original owner
	// coordinate as well as the artifact; both physical encodings are protected.
	if !allowed(ctx, opts, policy, root, main) || !allowed(ctx, opts, policy, root, main+".zst") || !allowed(ctx, opts, policy, root, path) {
		return sessioncapture.Request{}, "policy_denied"
	}
	if info.Mode()&0o111 != 0 {
		return sessioncapture.Request{}, "executable_source_requires_review"
	}
	sf := adapters.SessionFile{Agent: root.Agent, Root: root.Path, Path: path, Key: derivedKey(a, root.Path, relativeKey(root.Path, path)), SessionKey: legacy, Role: externalRole(parts[0])}
	sf, transform, code := classifyOwnedExternal(opts.Derived, root, a, path, sf)
	if code != "" {
		return sessioncapture.Request{}, code
	}
	q := emitRequest(opts, root, sf, path, observed, transform)
	q.Identity.SessionID = session
	q.Associations = append(q.Associations, "owner-uuid:"+uuid)
	return q, ""
}

// Known layout ownership replaces adapter.Classify for external artifacts, including
// derived probes: those probes must never re-enter the legacy global resolver.
func classifyOwnedExternal(derived bool, root Root, a adapters.Adapter, path string, sf adapters.SessionFile) (adapters.SessionFile, string, string) {
	ext := strings.ToLower(filepath.Ext(path))
	if adapters.IsCredential(filepath.Base(path)) {
		return sf, "", "policy_denied"
	}
	if scrub.Recognizes(path) {
		return classifyConfig(derived, root, a, path, sf, true)
	}
	if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" {
		if !derived {
			return sf, "", "sqlite_backup_required"
		}
		sf.Key = derivedKey(a, root.Path, "db/"+relativeKey(root.Path, path))
		sf.Role = adapters.RoleDB
		return sf, "sqlite-backup", ""
	}
	if compressed(sf, ext) {
		return sf, "", "unsupported_compressed_source"
	}
	return sf, "", ""
}
