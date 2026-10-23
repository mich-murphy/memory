package capturesource

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturepolicy"
)

func discoverCoordinates(ctx context.Context, opts Options) (Result, error) {
	out := Result{}
	routes := coordinateRoutes(opts)
	opts.EntriesPerRoot = effectiveEntryBudget(opts.EntriesPerRoot, len(routes)+1)
	opts.fdDepth = min(opts.MaxDepth, 256/max(1, len(routes)))
	binding := strings.Join([]string{opts.ArchiveID, opts.MachineID, opts.Home, opts.Coordinates.CursorFingerprint()}, "\x00")
	c := opts.Cursor
	if c == nil {
		c = &Cursor{binding: binding}
		for _, r := range routes {
			c.walks = append(c.walks, walk{root: Root{r.Agent, r.Root}, route: r.Path})
		}
	}
	if c.closed || c.binding != binding {
		return out, errors.New("capturesource: closed or mismatched cursor")
	}
	out.Cursor = c
	policy, err := capturepolicy.New(opts.ConfigPath, opts.StateDir)
	if err != nil || opts.Policy == nil {
		for _, w := range c.walks {
			out.Issues = append(out.Issues, Issue{w.root, w.route, "policy_unavailable"})
			out.Coverage = append(out.Coverage, Coverage{Root: w.root, State: "policy_unavailable"})
		}
		return out, ctx.Err()
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	for j := range c.walks {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		w := &c.walks[j]
		n, err := coordinateSlice(ctx, opts, policy, w, now, &out)
		if err != nil {
			return out, err
		}
		coordinateCoverage(w, n, &out)
	}
	if err := coordinateLeaves(ctx, opts, c, &out); err != nil {
		return out, err
	}
	if err := observePrimary(ctx, opts, c, &out); err != nil {
		return out, err
	}
	return out, nil
}

func startCoordinate(ctx context.Context, opts Options, policy *capturepolicy.Policy, w *walk, out *Result) bool {
	if opts.fdDepth < 1 {
		gap(w, w.route, "fd_budget", out)
		w.done = true
		return false
	}
	c, err := opts.Coordinates.Coordinate(w.root.Agent, w.route)
	if err != nil {
		gap(w, w.route, "outside_inventory", out)
		w.done = true
		return false
	}
	if !coordinatePolicy(ctx, opts, policy, c) {
		gap(w, w.route, "policy_denied", out)
		w.done = true
		return false
	}
	actual, code := resolvedCoordinate(opts, c)
	if code != "" {
		gap(w, w.route, code, out)
		w.done = true
		return false
	}
	st, err := os.Stat(actual)
	if err != nil || !st.IsDir() {
		gap(w, w.route, "root_not_directory_or_alias", out)
		w.done = true
		return false
	}
	f, err := os.Open(actual)
	if err != nil {
		gap(w, w.route, "root_unreadable", out)
		w.done = true
		return false
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		_ = f.Close()
		gap(w, w.route, "root_replaced", out)
		w.done = true
		return false
	}
	w.stack = append(w.stack, frame{w.route, f})
	return true
}

// Retained descriptors must still name the observed route on resume. Directory
// replacement does not turn an old descriptor into fresh complete coverage.
func coordinateFrame(ctx context.Context, opts Options, policy *capturepolicy.Policy, w *walk, out *Result) bool {
	top := w.stack[len(w.stack)-1]
	c, err := opts.Coordinates.Coordinate(w.root.Agent, top.path)
	if err != nil || !coordinatePolicy(ctx, opts, policy, c) {
		gap(w, top.path, "policy_denied", out)
		return false
	}
	actual, code := resolvedCoordinate(opts, c)
	if code != "" {
		gap(w, top.path, code, out)
		return false
	}
	current, err := os.Stat(actual)
	opened, openErr := top.file.Stat()
	if err != nil || openErr != nil || !os.SameFile(current, opened) {
		gap(w, top.path, "root_replaced", out)
		return false
	}
	return true
}

func coordinateSlice(ctx context.Context, opts Options, policy *capturepolicy.Policy, w *walk, now func() time.Time, out *Result) (int, error) {
	if !w.done && len(w.stack) == 0 {
		startCoordinate(ctx, opts, policy, w, out)
	}
	n := 0
	for !w.done && n < opts.EntriesPerRoot {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if !coordinateFrame(ctx, opts, policy, w, out) {
			for len(w.stack) > 0 {
				pop(w)
			}
			break
		}
		top := w.stack[len(w.stack)-1]
		entries, err := top.file.ReadDir(1)
		if len(entries) == 0 {
			pop(w)
			coordinateReadError(w, top.path, err, out)
			continue
		}
		n++
		w.total++
		path := filepath.Join(top.path, entries[0].Name())
		cc, e := opts.Coordinates.Coordinate(w.root.Agent, path)
		if e != nil {
			gap(w, path, "outside_inventory", out)
			continue
		}
		// Nested ownership is computed after projection, not by physical prefix.
		if cc.Root != w.root.Path {
			continue
		}
		coordinateVisit(ctx, opts, policy, w, cc, entries[0], now().UnixNano(), out)
	}
	return n, nil
}

func coordinateVisit(ctx context.Context, opts Options, policy *capturepolicy.Policy, w *walk, c capturecoordinates.Coordinate, entry os.DirEntry, observed int64, out *Result) {
	if len(strings.Split(c.Path, string(filepath.Separator))) > 64 {
		gap(w, c.Path, "depth_budget", out)
		return
	}
	if !coordinatePolicy(ctx, opts, policy, c) {
		gap(w, c.Path, "policy_denied", out)
		return
	}

	if entry.IsDir() {
		if len(w.stack) >= opts.fdDepth {
			code := "depth_budget"
			if opts.fdDepth < opts.MaxDepth {
				code = "fd_budget"
			}
			gap(w, c.Path, code, out)
			return
		}
		actual, code := resolvedCoordinate(opts, c)
		if code != "" {
			gap(w, c.Path, code, out)
			return
		}
		f, err := os.Open(actual)
		if err != nil {
			gap(w, c.Path, "directory_unreadable", out)
			return
		}
		w.stack = append(w.stack, frame{c.Path, f})
		return
	}
	st, code := coordinateInfo(ctx, opts, policy, c, false)
	if code != "" {
		gap(w, c.Path, code, out)
		return
	}
	if !st.Mode().IsRegular() {
		gap(w, c.Path, "nonregular_source", out)
		return
	}
	q, code := classifyCoordinateInfo(ctx, opts, policy, c, st, observed)
	if code != "" {
		gap(w, c.Path, code, out)
		return
	}
	out.Requests = append(out.Requests, q)
}

func coordinateLeaves(ctx context.Context, opts Options, c *Cursor, out *Result) error {
	leaves := opts.Coordinates.Leaves()
	end := min(len(leaves), c.leafIndex+opts.EntriesPerRoot)
	for c.leafIndex < end {
		if err := ctx.Err(); err != nil {
			return err
		}
		leaf := leaves[c.leafIndex]
		c.leafIndex++
		cc, err := opts.Coordinates.Coordinate(leaf.Agent, leaf.Path)
		if err != nil {
			continue
		} // compiler guarantees valid exact declarations
		w := walk{root: Root{cc.Agent, cc.Root}}
		q, err := classifyCoordinate(ctx, opts, leaf.Agent, leaf.Path, false)
		if err == nil {
			out.Requests = append(out.Requests, q)
		} else if issue, ok := errors.AsType[*ClassificationError](err); ok {
			gap(&w, leaf.Path, issue.Issue.Code, out)
		} else {
			return err
		}
		state := w.state
		if state == "" {
			state = "observed_complete"
		}
		out.Coverage = append(out.Coverage, Coverage{Root: w.root, State: state, Examined: 1, Enrichment: "not_integrated"})
	}
	if c.leafIndex < len(leaves) {
		out.Issues = append(out.Issues, Issue{Code: "leaf_budget"})
		for _, leaf := range leaves[c.leafIndex:] {
			cc, err := opts.Coordinates.Coordinate(leaf.Agent, leaf.Path)
			if err == nil {
				out.Coverage = append(out.Coverage, Coverage{Root: Root{cc.Agent, cc.Root}, State: "bounded"})
			}
		}
	}
	return nil
}

// Nested logical roots also have inherited spellings beneath parent routes.
// Enumerate those exact root projections so pruning cannot hide their subtrees.
func coordinateRoutes(opts Options) []capturecoordinates.RootRoute {
	routes := opts.Coordinates.Routes()
	seen := map[capturecoordinates.RootRoute]bool{}
	out := []capturecoordinates.RootRoute{}
	for _, r := range routes {
		c, err := opts.Coordinates.Coordinate(r.Agent, r.Root)
		if err != nil {
			continue
		}
		for _, path := range opts.Coordinates.EquivalentPaths(c) {
			route := capturecoordinates.RootRoute{Agent: c.Agent, Root: c.Root, Path: path}
			if !seen[route] {
				seen[route] = true
				out = append(out, route)
			}
		}
	}
	return out
}

func coordinateCoverage(w *walk, n int, out *Result) {
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
	out.Issues = append(out.Issues, Issue{w.root, w.route, "launch_snapshot_integration_required"})
}

func coordinateReadError(w *walk, path string, err error, out *Result) {
	if err != nil && !errors.Is(err, io.EOF) {
		gap(w, path, "directory_unreadable", out)
	}
}
