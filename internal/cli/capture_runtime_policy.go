package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func runtimePolicy(c capturedaemon.Config) func(context.Context, sessioncapture.Request) error {
	return runtimePolicyMode(c, false)
}

// Discovery admits only metadata for a recognized configuration. The queue still
// requires the explicit scrubbed-config transform before reading protected bytes.
func runtimeDiscoveryPolicy(c capturedaemon.Config) func(context.Context, sessioncapture.Request) error {
	return runtimePolicyMode(c, true)
}

func runtimePolicyMode(c capturedaemon.Config, discovery bool) func(context.Context, sessioncapture.Request) error {
	return func(ctx context.Context, req sessioncapture.Request) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if req.Identity.ArchiveID != c.ArchiveID || req.Identity.MachineID != c.MachineID || !c.AllowsSource(req.Identity.Agent, req.Path) {
			return errors.New("source outside configured inventory")
		}
		if req.Identity.SessionID != "" {
			if err := capturebackend.ValidateIdentity(c.BackendSpec(), req.Identity); err != nil {
				return err
			}
		}
		if os.Getenv("AGENT_SESSIONS_DISABLE") != "" {
			return errors.New("capture disabled by effective environment policy")
		}
		if err := runtimeUnresolvedNativePolicy(c, req.Identity.Agent); err != nil {
			return errors.Join(sessioncapture.ErrSessionExcluded, err)
		}
		if err := runtimeExclusions(ctx, c, req, discovery); err != nil {
			return err
		}
		return runtimeFilesystemPolicy(c, req.Path, discovery || req.Transform == "scrubbed-config")
	}
}

func runtimeExclusions(ctx context.Context, c capturedaemon.Config, req sessioncapture.Request, discovery bool) error {
	for _, dir := range []string{c.PolicyDir, filepath.Join(c.PolicyDir, "exclusions")} {
		if err := capturedaemon.SafePath(dir); err != nil {
			return errors.New("effective exclusions unavailable")
		}
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return errors.New("effective exclusions unavailable")
		}
	}
	registry, err := exclusion.Load(c.PolicyDir)
	if err != nil {
		return errors.New("effective exclusions unavailable")
	}
	claimedExcluded, err := runtimeClaimedSessionExcluded(c, req, registry)
	if err != nil {
		return errors.New("effective claimed-session exclusions unavailable")
	}
	if claimedExcluded {
		return sessioncapture.ErrSessionExcluded
	}
	sf, err := runtimePrivacyOwner(ctx, c, req, discovery)
	if err != nil {
		return err
	}
	// A directory is a traversal scope, not an aggregate artifact. Applying an
	// empty session key here would let one opt-out suppress unrelated sessions.
	if sf == nil {
		return nil
	}
	excluded := registry.Excludes(*sf)
	if runtimeAliasMapped(c, req.Identity.Agent, req.Path) {
		aliasExcluded, err := runtimeAliasExcluded(c, req, *sf, registry)
		if err != nil {
			return err
		}
		excluded = excluded || aliasExcluded
	}
	if excluded {
		if sf.SessionKey != "" {
			return sessioncapture.ErrSessionExcluded
		}
		return errors.New("session excluded by effective policy")
	}
	return nil
}

// runtimePrivacyOwner derives legacy policy ownership only from the configured
// root and physical layout. Request identity/associations are untrusted hints.
// In particular it never invokes adapter root inference or Claude's external
// ownership index (which scans the entire projects tree).
func runtimePrivacyOwner(ctx context.Context, c capturedaemon.Config, req sessioncapture.Request, discovery bool) (*adapters.SessionFile, error) {
	if runtimeAliasMapped(c, req.Identity.Agent, req.Path) {
		return runtimeAliasPrivacyOwner(ctx, c, req, discovery)
	}
	root := runtimeOwningRoot(c, req.Identity.Agent, req.Path)
	if root == "" {
		return nil, errors.New("source outside configured inventory")
	}
	st, err := os.Lstat(req.Path)
	if err == nil && st.IsDir() && req.Role == "" {
		return nil, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("source metadata unavailable")
	}
	if err == nil && !st.Mode().IsRegular() {
		return nil, errors.New("source is not a regular artifact")
	}
	// A missing discovery root is not a captured aggregate. Missing accepted
	// files still classify without I/O, preserving opt-outs before delivery.
	if errors.Is(err, os.ErrNotExist) && req.Role == "" && req.Path == root {
		return nil, nil
	}
	rel, _ := filepath.Rel(root, req.Path)
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if req.Identity.Agent == "claude" && runtimeExternalScope(first) {
		return runtimeExternalPrivacyOwner(ctx, c, req, root, rel, discovery)
	}
	a := adapters.All(c.Home)[req.Identity.Agent]
	if a == nil {
		return nil, errors.New("source agent unsupported")
	}
	sf, ok := a.Classify(req.Path, root)
	if !ok {
		// SQLite and other adapter omissions have no proven session ownership.
		// Conservatively treat them as root-wide; never fall back to path alone.
		sf = adapters.SessionFile{Agent: req.Identity.Agent, Root: root, Path: req.Path}
	}
	return &sf, nil
}

// Prefer the longest actual configured root; the one-file global Claude config
// exception uses the canonical Claude namespace only when no root owns it.
func runtimeOwningRoot(c capturedaemon.Config, agent, path string) string {
	if runtimeHasAliases(c) {
		inventory, err := c.CoordinateInventory()
		if err != nil {
			return ""
		}
		if coordinate, err := inventory.Coordinate(agent, path); err == nil {
			return coordinate.Root
		}
	}
	root := ""
	for _, r := range c.SourceRoots() {
		if r.Agent == agent && (path == r.Path || strings.HasPrefix(path, r.Path+string(filepath.Separator))) && len(r.Path) > len(root) {
			root = r.Path
		}
	}
	if root == "" && c.DefaultRoots && agent == "claude" && path == filepath.Join(c.Home, ".claude.json") {
		return filepath.Join(c.Home, ".claude")
	}
	return root
}

func runtimeFilesystemPolicy(c capturedaemon.Config, path string, scrubbed bool) error {
	resolved, err := capturepolicy.Resolve(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("source policy resolution unavailable")
	}
	// Built fresh per call: no caching across calls, entries, batches or sweeps.
	p, err := capturepolicy.NewSet(
		[]string{c.ConfigPath(), filepath.Join(c.Home, ".config", "agent-sessions", "config.json")},
		c.LocalProtectedPaths())
	if err != nil {
		return errors.New("source excluded by filesystem policy")
	}
	excluded := p.Excludes(resolved.Identities)
	if scrubbed {
		excluded = p.ExcludesClaudeConfig(resolved.Identities)
	}
	if excluded {
		return errors.New("source excluded by filesystem policy")
	}
	return nil
}
