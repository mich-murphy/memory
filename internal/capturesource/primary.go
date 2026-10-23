package capturesource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func primaryPath(opts Options) string { return filepath.Join(opts.Home, ".claude.json") }

func standalonePrimary(opts Options, agent, path string) bool {
	return opts.Derived && !opts.OnlyRoots && agent == "claude" && path == primaryPath(opts)
}

// classifyPrimary is an exact-file exception, not a Home root. Never enumerate
// Home or consult adapter external discovery. Queue alone reads/scrubs the bytes.
func classifyPrimary(ctx context.Context, opts Options) (sessioncapture.Request, error) {
	root := Root{"claude", filepath.Join(opts.Home, ".claude")}
	path := primaryPath(opts)
	fail := func(code string) (sessioncapture.Request, error) {
		return sessioncapture.Request{}, &ClassificationError{Issue{root, path, code}}
	}
	if err := ctx.Err(); err != nil {
		return sessioncapture.Request{}, err
	}
	policy, err := capturepolicy.New(opts.ConfigPath, opts.StateDir)
	if err != nil || opts.Policy == nil {
		return fail("policy_unavailable")
	}
	// Callback on the original path precedes resolution/metadata. allowed repeats
	// it with every resolved identity before this exact-file exception is admitted.
	if !allowed(ctx, opts, policy, root, path) {
		return fail("policy_denied")
	}
	parent, err := os.Lstat(opts.Home)
	if err != nil {
		return fail("source_unreadable")
	}
	if !parent.IsDir() {
		return fail("alias_integration_required")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fail("observed_absence")
	}
	if err != nil {
		return fail("source_unreadable")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fail("alias_integration_required")
	}
	if !info.Mode().IsRegular() {
		return fail("nonregular_source")
	}
	if info.Mode()&0o111 != 0 {
		return fail("executable_source_requires_review")
	}
	if !allowed(ctx, opts, policy, root, path) {
		return fail("policy_denied")
	}
	if err := ctx.Err(); err != nil {
		return sessioncapture.Request{}, err
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	sf := adapters.SessionFile{Agent: "claude", Root: root.Path, Path: path, Key: "config/claude.json", Role: adapters.RoleConfig}
	return emitRequest(opts, root, sf, path, now().UnixNano(), "scrubbed-config"), nil
}

func observePrimary(ctx context.Context, opts Options, c *Cursor, out *Result) error {
	if c.primaryDone || !standalonePrimary(opts, "claude", primaryPath(opts)) || owningRoot(opts.roots, "claude", primaryPath(opts)).Path != "" {
		return nil
	}
	q, err := classifyPrimary(ctx, opts)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.primaryDone = true
	if err == nil {
		out.Requests = append(out.Requests, q)
		return nil
	}
	if gap, ok := errors.AsType[*ClassificationError](err); ok {
		out.Issues = append(out.Issues, gap.Issue)
		return nil
	}
	return err
}
