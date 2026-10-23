// Package capturesource provides bounded, read-only source discovery. It never reads
// transcript bodies, writes collector state, or materializes derived sources.
package capturesource

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Root is an explicit learned root. Defaults are canonical paths under Home, not
// environment-derived paths or settings discovered by reading protected config.
type Root struct{ Agent, Path string }

// Scope is a policy coordinate, not source content. Policy is called before
// discovery and again with all resolved identities before admitting a source.
type Scope struct {
	Root       Root
	Path       string
	Identities []string
}

type Options struct {
	ArchiveID, MachineID, Home string
	Roots                      []Root
	// Coordinates opts into declared logical/observed path equivalence. Nil preserves legacy discovery.
	Coordinates *capturecoordinates.Inventory
	// OnlyRoots disables canonical defaults (useful for scoped hook hints).
	OnlyRoots bool
	// Derived opts into queue-owned bounded transforms; discovery never prepares bytes.
	Derived              bool
	ConfigPath, StateDir string
	Policy               func(context.Context, Scope) error
	// LookupOwner performs one bounded durable root/UUID lookup, never a source scan.
	// Nil preserves explicit unsupported external-artifact coverage.
	LookupOwner func(context.Context, Root, string) (sessionID, legacySessionKey string, err error)
	// EntriesPerRoot requests a per-root entry budget. Each root receives the
	// smaller of this and floor(4096/root count), bounding the whole call.
	// MaxDepth bounds open directory handles per root.
	EntriesPerRoot, MaxDepth int
	Cursor                   *Cursor
	fdDepth                  int
	roots                    []Root
	Now                      func() time.Time
}

// Issue intentionally contains no OS error messages or source contents. Path is
// an explicit local coordinate; callers must treat coordinates as private metadata.
type (
	Issue struct {
		Root       Root
		Path, Code string
	}
	Coverage struct {
		Root       Root
		State      string
		Examined   int
		Enrichment string
	}
	Result struct {
		Requests []sessioncapture.Request
		Issues   []Issue
		Coverage []Coverage
		Cursor   *Cursor
	}
)

type (
	frame struct {
		path string
		file *os.File
	}
	walk struct {
		root  Root
		route string
		stack []frame
		done  bool
		state string
		total int
	}
)

// Cursor owns read-only directory descriptors. Resume with the same options;
// Close on completion, cancellation, replacement, or shutdown. It is in-process,
// not a durable checkpoint, and must not be used concurrently. Native directory
// order is retained across calls; roots retain input order and are independently budgeted.
// A restart starts a new observation; absence never authorizes deletion.
type Cursor struct {
	binding     string
	walks       []walk
	closed      bool
	primaryDone bool
	leafIndex   int
}

func (c *Cursor) Close() error {
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	var errs []error
	for i := range c.walks {
		for _, f := range c.walks[i].stack {
			errs = append(errs, f.file.Close())
		}
		c.walks[i].stack = nil
	}
	return errors.Join(errs...)
}

// Discover returns safe raw requests alongside independent partial coverage.
// A non-nil error indicates invalid options or cancellation, not a root failure;
// requests already returned remain useful. Queue policy must still be rechecked
// at capture and delivery. Requests describe observations, not owned bytes.
func Discover(ctx context.Context, opts Options) (Result, error) {
	out := Result{}
	if err := defaults(&opts); err != nil {
		return out, err
	}
	if opts.Coordinates != nil {
		return discoverCoordinates(ctx, opts)
	}
	roots, err := rootsFor(opts)
	if err != nil {
		return out, err
	}
	opts.roots = roots
	opts.EntriesPerRoot = effectiveEntryBudget(opts.EntriesPerRoot, len(roots))
	opts.fdDepth = min(opts.MaxDepth, 256/max(1, len(roots)))
	parts := []string{opts.ArchiveID, opts.MachineID, opts.Home}
	for _, r := range roots {
		parts = append(parts, r.Agent, r.Path)
	}
	binding := strings.Join(parts, "\x00")
	c := opts.Cursor
	if c == nil {
		c = &Cursor{binding: binding}
		for _, r := range roots {
			c.walks = append(c.walks, walk{root: r})
		}
	}
	if c.closed || c.binding != binding {
		return out, errors.New("capturesource: closed or mismatched cursor")
	}
	out.Cursor = c
	policy, err := capturepolicy.New(opts.ConfigPath, opts.StateDir)
	if err != nil || opts.Policy == nil {
		for _, r := range roots {
			out.Issues = append(out.Issues, Issue{r, r.Path, "policy_unavailable"})
			out.Coverage = append(out.Coverage, Coverage{Root: r, State: "policy_unavailable"})
		}
		err = observePrimary(ctx, opts, c, &out)
		return out, err
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	agents := adapters.All(opts.Home)
	for i := range c.walks {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		w := &c.walks[i]
		if err := discoverRoot(ctx, opts, policy, w, agents[w.root.Agent], now, &out); err != nil {
			return out, err
		}

	}
	if err := observePrimary(ctx, opts, c, &out); err != nil {
		return out, err
	}

	return out, nil
}

func rootsFor(opts Options) ([]Root, error) {
	roots := append([]Root(nil), opts.Roots...)
	if !opts.OnlyRoots {
		for _, agent := range []string{"claude", "codex", "pi"} {
			p := filepath.Join(opts.Home, "."+agent)
			if agent == "pi" {
				p = filepath.Join(p, "agent")
			}
			roots = append(roots, Root{agent, p})
		}
	}
	// Preserve explicit root order; independent budgets make an early root unable
	// to consume a later root's allowance. Exact duplicate namespaces are coalesced.
	seen := map[Root]bool{}
	var out []Root
	for _, r := range roots {
		if !filepath.IsAbs(r.Path) || (r.Agent != "claude" && r.Agent != "codex" && r.Agent != "pi") || strings.Contains(r.Path, "\x00") {
			return nil, errors.New("capturesource: invalid root")
		}
		r.Path = filepath.Clean(r.Path)
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	if len(out) > 128 {
		return nil, errors.New("capturesource: too many roots")
	}
	return out, nil
}

func allowed(ctx context.Context, opts Options, policy *capturepolicy.Policy, r Root, path string) bool {
	if opts.Policy(ctx, Scope{Root: r, Path: path, Identities: []string{path}}) != nil {
		return false
	}
	resolution, err := capturepolicy.Resolve(path)
	excluded := policy.Excludes(resolution.Identities)
	if opts.Derived && filepath.Base(path) == ".claude.json" && scrub.Recognizes(path) {
		excluded = policy.ExcludesClaudeConfig(resolution.Identities)
	}
	if excluded {
		return false
	}
	// An absent root is an observation, not a policy failure.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return true
	}
	return opts.Policy(ctx, Scope{Root: r, Path: path, Identities: resolution.Identities}) == nil
}

func start(w *walk, out *Result) {
	info, err := os.Lstat(w.root.Path)
	if err != nil {
		w.done = true
		w.state = "unavailable"
		code := "root_unreadable"
		if errors.Is(err, os.ErrNotExist) {
			w.state = "observed_absent"
			code = "observed_absence"
		}
		out.Issues = append(out.Issues, Issue{w.root, w.root.Path, code})
		return
	}
	if !info.IsDir() {
		w.done = true
		w.state = "partial"
		out.Issues = append(out.Issues, Issue{w.root, w.root.Path, "root_not_directory_or_alias"})
		return
	}
	f, err := os.Open(w.root.Path)
	if err != nil {
		w.done = true
		w.state = "unavailable"
		out.Issues = append(out.Issues, Issue{w.root, w.root.Path, "root_unreadable"})
		return
	}
	w.stack = append(w.stack, frame{w.root.Path, f})
}

func pop(w *walk) {
	top := w.stack[len(w.stack)-1]
	_ = top.file.Close() // read-only directory
	w.stack = w.stack[:len(w.stack)-1]
	if len(w.stack) == 0 {
		w.done = true
	}
}

func visit(ctx context.Context, opts Options, policy *capturepolicy.Policy, w *walk, a adapters.Adapter, e os.DirEntry, path string, observed int64, out *Result) {
	if len(strings.Split(path, string(filepath.Separator))) > 64 {
		gap(w, path, "depth_budget", out)
		return
	}
	if e.Type()&os.ModeSymlink != 0 {
		gap(w, path, "alias_integration_required", out)
		return
	}
	if e.IsDir() {
		if len(w.stack) >= opts.fdDepth {
			code := "depth_budget"
			if opts.fdDepth < opts.MaxDepth {
				code = "fd_budget"
			}
			gap(w, path, code, out)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			gap(w, path, "directory_unreadable", out)
			return
		}
		w.stack = append(w.stack, frame{path, f})
		return
	}
	info, err := e.Info()
	if err != nil {
		gap(w, path, "source_unreadable", out)
		return
	}
	if !info.Mode().IsRegular() {
		gap(w, path, "nonregular_source", out)
		return
	}
	q, code := classifyInfo(ctx, opts, policy, w.root, a, path, info, observed)
	if code != "" {
		gap(w, path, code, out)
		return
	}
	out.Requests = append(out.Requests, q)
}

func gap(w *walk, path, code string, out *Result) {
	w.state = "partial"
	out.Issues = append(out.Issues, Issue{w.root, path, code})
}

func discoverRoot(ctx context.Context, opts Options, policy *capturepolicy.Policy, w *walk, agent adapters.Adapter, now func() time.Time, out *Result) error {
	// Policy recovery/revocation is checked even on an already opened root.
	if !allowed(ctx, opts, policy, w.root, w.root.Path) {
		out.Issues = append(out.Issues, Issue{w.root, w.root.Path, "policy_denied"})
		out.Coverage = append(out.Coverage, Coverage{Root: w.root, State: "excluded"})
		return nil
	}
	if !w.done && len(w.stack) == 0 {
		start(w, out)
	}
	n, err := walkSlice(ctx, opts, policy, w, agent, now, out)
	if err != nil {
		return err
	}

	state := w.state
	if !w.done {
		state = "bounded"
	}
	if state == "" {
		state = "observed_complete"
		if w.total == 0 {
			state = "observed_empty"
		}
	}
	out.Coverage = append(out.Coverage, Coverage{Root: w.root, State: state, Examined: n, Enrichment: "not_integrated"})
	out.Issues = append(out.Issues, Issue{w.root, w.root.Path, "launch_snapshot_integration_required"})
	if w.root.Agent == "claude" && (!opts.Derived || opts.OnlyRoots) {
		out.Issues = append(out.Issues, Issue{w.root, w.root.Path, "protected_export_integration_required"})
	}
	return nil
}

func walkSlice(ctx context.Context, opts Options, policy *capturepolicy.Policy, w *walk, agent adapters.Adapter, now func() time.Time, out *Result) (int, error) {
	n := 0
	for !w.done && n < opts.EntriesPerRoot {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		top := &w.stack[len(w.stack)-1]
		entries, readErr := top.file.ReadDir(1)
		if len(entries) == 0 {
			pop(w)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				out.Issues = append(out.Issues, Issue{w.root, top.path, "directory_unreadable"})
				w.state = "partial"
			}
			continue
		}
		n++
		w.total++
		entry := entries[0]
		path := filepath.Join(top.path, entry.Name())
		if owningRoot(opts.roots, w.root.Agent, path) != w.root {
			continue // independently inventoried by its more specific root
		}
		if !allowed(ctx, opts, policy, w.root, path) {
			gap(w, path, "policy_denied", out)
			continue
		}
		visit(ctx, opts, policy, w, agent, entry, path, now().UnixNano(), out)
	}
	return n, nil
}

func compressed(sf adapters.SessionFile, ext string) bool {
	if sf.Zstd {
		return true
	}
	switch ext {
	case ".zst", ".zstd", ".gz", ".bz2", ".xz", ".zip", ".7z", ".rar", ".tar", ".tgz":
		return true
	default:
		return false
	}
}

func defaults(opts *Options) error {
	if opts.ArchiveID == "" || opts.MachineID == "" || !filepath.IsAbs(opts.Home) {
		return errors.New("capturesource: explicit archive, machine and absolute home required")
	}
	if opts.EntriesPerRoot <= 0 {
		opts.EntriesPerRoot = 256
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 32
	}
	if opts.EntriesPerRoot > 4096 || opts.MaxDepth > 64 {
		return errors.New("capturesource: budget exceeds limit")
	}
	return nil
}

// effectiveEntryBudget allocates a fair independent slice to every root rather
// than allowing early roots to consume a shared pool and starve later roots.
// rootsFor limits rootCount to 128; requested has already passed defaults.
func effectiveEntryBudget(requested, rootCount int) int {
	return min(requested, max(1, 4096/max(1, rootCount)))
}
