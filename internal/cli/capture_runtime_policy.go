package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Discovery admits only metadata for a recognized configuration. The queue still
// requires the explicit scrubbed-config transform before reading protected bytes.
func (r runtimeComposition) policy(discovery bool) func(context.Context, sessioncapture.Request) error {
	c := r.config
	return r.policyWith(discovery, r.loadRegistry, r.newFilesystemSet, func(agent string) error { return runtimeUnresolvedNativePolicy(c, agent) })
}

// snapshotPolicy is policy(discovery) with the exclusion registry and the
// filesystem policy set built at most once, on first use. A discovery call owns one
// snapshot so a 4096-entry walk costs one registry load, not one per entry. It is
// only a pre-filter: every admitted request is rechecked live by policy(false) at
// capture, so an exclusion saved after the snapshot still prevents capture and the
// next call takes a fresh snapshot.
func (r runtimeComposition) snapshotPolicy(discovery bool) func(context.Context, sessioncapture.Request) error {
	c := r.config
	memoSet := sync.OnceValues(func() (*capturepolicy.Policy, error) {
		p, err := r.newFilesystemSet()
		if err != nil {
			return nil, err
		}
		return p.Memoized(), nil
	})
	return r.policyWith(discovery, sync.OnceValues(r.loadRegistry), memoSet, perAgent(func(agent string) error { return runtimeUnresolvedNativePolicy(c, agent) }))
}

// perAgent evaluates check once per agent for the lifetime of the returned func.
// Only a snapshot owner (one discovery call) may hold it; see snapshotPolicy.
func perAgent(check func(string) error) func(string) error {
	var mu sync.Mutex
	results := map[string]error{}
	return func(agent string) error {
		mu.Lock()
		defer mu.Unlock()
		err, ok := results[agent]
		if !ok {
			err = check(agent)
			results[agent] = err
		}
		return err
	}
}

func (r runtimeComposition) policyWith(discovery bool, registry func() (exclusion.Registry, error), filesystem func() (*capturepolicy.Policy, error), native func(string) error) func(context.Context, sessioncapture.Request) error {
	c := r.config
	return func(ctx context.Context, req sessioncapture.Request) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if req.Identity.ArchiveID != c.ArchiveID || req.Identity.MachineID != c.MachineID || !c.AllowsSourceIn(r.inventory, req.Identity.Agent, req.Path) {
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
		if err := native(req.Identity.Agent); err != nil {
			return errors.Join(sessioncapture.ErrSessionExcluded, err)
		}
		if err := r.exclusions(ctx, req, discovery, registry); err != nil {
			return err
		}
		return runtimeFilesystemPolicyWith(req.Path, discovery || req.Transform == "scrubbed-config", capturepolicy.Resolve, filesystem)
	}
}

// loadRegistry validates the policy directories and reads the exclusion registry.
func (r runtimeComposition) loadRegistry() (exclusion.Registry, error) {
	c := r.config
	for _, dir := range []string{c.PolicyDir, filepath.Join(c.PolicyDir, "exclusions")} {
		if err := capturedaemon.SafePath(dir); err != nil {
			return nil, sessioncapture.ErrPolicyUnavailable
		}
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return nil, sessioncapture.ErrPolicyUnavailable
		}
	}
	registry, err := r.loadExclusions(c.PolicyDir)
	if err != nil {
		return nil, sessioncapture.ErrPolicyUnavailable
	}
	return registry, nil
}

func (r runtimeComposition) exclusions(ctx context.Context, req sessioncapture.Request, discovery bool, load func() (exclusion.Registry, error)) error {
	registry, err := load()
	if err != nil {
		return err
	}
	claimedExcluded, err := r.claimedSessionExcluded(req, registry)
	if err != nil {
		return errors.New("effective claimed-session exclusions unavailable")
	}
	if claimedExcluded {
		return sessioncapture.ErrSessionExcluded
	}
	sf, err := r.privacyOwner(ctx, req, discovery)
	if err != nil {
		return err
	}
	// A directory is a traversal scope, not an aggregate artifact. Applying an
	// empty session key here would let one opt-out suppress unrelated sessions.
	if sf == nil {
		return nil
	}
	excluded := registry.Excludes(*sf)
	if r.aliasMapped(req.Identity.Agent, req.Path) {
		aliasExcluded, err := r.aliasExcluded(req, *sf, registry)
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
func (r runtimeComposition) privacyOwner(ctx context.Context, req sessioncapture.Request, discovery bool) (*adapters.SessionFile, error) {
	c := r.config
	if r.aliasMapped(req.Identity.Agent, req.Path) {
		return r.aliasPrivacyOwner(ctx, req, discovery)
	}
	root := r.owningRoot(req.Identity.Agent, req.Path)
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
		return r.externalPrivacyOwner(ctx, req, root, rel, discovery)
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
func (r runtimeComposition) owningRoot(agent, path string) string {
	c := r.config
	if runtimeHasAliases(c) {
		inventory := r.inventory
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
	// Built fresh per call: no caching across calls, entries, batches or sweeps.
	return runtimeFilesystemPolicyWith(path, scrubbed, capturepolicy.Resolve, func() (*capturepolicy.Policy, error) {
		return newRuntimeFilesystemSet(c)
	})
}

func (r runtimeComposition) newFilesystemSet() (*capturepolicy.Policy, error) {
	if r.filesystemSet != nil {
		return r.filesystemSet(r.config)
	}
	return newRuntimeFilesystemSet(r.config)
}

func (r runtimeComposition) loadExclusions(dir string) (exclusion.Registry, error) {
	if r.registryLoader != nil {
		return r.registryLoader(dir)
	}
	return exclusion.Load(dir)
}

func newRuntimeFilesystemSet(c capturedaemon.Config) (*capturepolicy.Policy, error) {
	return capturepolicy.NewSet(
		[]string{c.ConfigPath(), filepath.Join(c.Home, ".config", "agent-sessions", "config.json")},
		c.LocalProtectedPaths())
}

// runtimeFilesystemPolicyWith resolves path, then obtains the policy from policy(),
// preserving the order: resolution errors surface before policy construction errors.
// The caller owns any reuse of policy within a single call.
func runtimeFilesystemPolicyWith(path string, scrubbed bool, resolve func(string) (capturepolicy.Resolution, error), policy func() (*capturepolicy.Policy, error)) error {
	resolved, err := resolve(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("source policy resolution unavailable")
	}
	p, err := policy()
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
