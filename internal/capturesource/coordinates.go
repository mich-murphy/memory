package capturesource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// coordinatePolicy checks the union, not merely the successful target. An absent
// logical spelling is expected after a declared move and does not change identity.
func coordinatePolicy(ctx context.Context, opts Options, policy *capturepolicy.Policy, c capturecoordinates.Coordinate) bool {
	identities := []string{c.LogicalPath, c.Path}
	if opts.Policy == nil || opts.Policy(ctx, Scope{Root: Root{c.Agent, c.Root}, Path: c.Path, Identities: identities}) != nil {
		return false
	}
	identities = coordinateIdentities(opts, c)
	root := Root{c.Agent, c.Root}
	if opts.Policy == nil || opts.Policy(ctx, Scope{Root: root, Path: c.Path, Identities: identities}) != nil {
		return false
	}
	if opts.Derived && filepath.Base(c.LogicalPath) == ".claude.json" {
		return !policy.ExcludesClaudeConfig(identities)
	}
	return !policy.Excludes(identities)
}

// resolvedCoordinate authorizes every full suffix identity encountered by Resolve.
// A declaration is never blanket permission to follow links outside its coordinate.
func resolvedCoordinate(opts Options, c capturecoordinates.Coordinate) (string, string) {
	r, err := capturepolicy.Resolve(c.Path)
	for _, identity := range r.Identities {
		got, e := opts.Coordinates.Coordinate(c.Agent, identity)
		if e != nil || got.Root != c.Root || got.LogicalPath != c.LogicalPath {
			return "", "alias_integration_required"
		}
	}
	if err != nil {
		return "", "source_unreadable"
	}
	return r.Path, ""
}

func coordinateRoute(opts Options, c capturecoordinates.Coordinate) string {
	best := ""
	for _, r := range opts.Coordinates.Routes() {
		if r.Agent == c.Agent && within(r.Path, c.Path) && len(r.Path) > len(best) {
			best = r.Path
		}
	}
	return best
}

// coordinateInfo permits root-entry links and exact leaves only. Descendant
// directory symlinks are not traversal authority, even if their target is safe.
func coordinateInfo(ctx context.Context, opts Options, policy *capturepolicy.Policy, c capturecoordinates.Coordinate, native bool) (os.FileInfo, string) {
	if !coordinatePolicy(ctx, opts, policy, c) {
		return nil, "policy_denied"
	}
	if code := coordinateAncestors(ctx, opts, policy, c, native); code != "" {
		return nil, code
	}

	actual, code := resolvedCoordinate(opts, c)
	if code != "" {
		return nil, code
	}
	st, err := os.Stat(actual)
	if err != nil {
		return nil, "source_unreadable"
	}
	return st, ""
}

func classifyCoordinate(ctx context.Context, opts Options, agent, path string, native bool) (sessioncapture.Request, error) {
	var zero sessioncapture.Request
	root := Root{Agent: agent}
	fail := func(code string) (sessioncapture.Request, error) {
		return zero, &ClassificationError{Issue{root, path, code}}
	}
	if !filepath.IsAbs(path) || strings.Contains(path, "\x00") {
		return fail("absolute_path_required")
	}
	path = filepath.Clean(path)
	if len(strings.Split(path, string(filepath.Separator))) > 64 {
		return fail("depth_budget")
	}
	c, err := opts.Coordinates.Coordinate(agent, path)
	if err != nil {
		if standalonePrimary(opts, agent, path) {
			return classifyPrimary(ctx, opts)
		}
		return fail("outside_inventory")
	}
	root = Root{c.Agent, c.Root}
	policy, err := capturepolicy.New(opts.ConfigPath, opts.StateDir)
	if err != nil || opts.Policy == nil {
		return fail("policy_unavailable")
	}
	if !coordinatePolicy(ctx, opts, policy, c) {
		return fail("policy_denied")
	}
	c = availableCoordinate(opts, c)
	info, code := coordinateInfo(ctx, opts, policy, c, native)
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	if nativeMissing(native, code) {
		request, err := absentCoordinate(ctx, opts, policy, c)
		if err == nil {
			request.Identities = append(request.Identities, path)
		}
		return request, err
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
	q, code := classifyCoordinateInfo(ctx, opts, policy, c, info, now().UnixNano())
	if code != "" {
		return fail(code)
	}
	q.Identities = append(q.Identities, path)
	return q, nil
}

func classifyCoordinateInfo(ctx context.Context, opts Options, policy *capturepolicy.Policy, c capturecoordinates.Coordinate, info os.FileInfo, observed int64) (sessioncapture.Request, string) {
	// All adapter classification (including external ownership and transforms) sees
	// only logical names. The returned pathname remains the original observed byte source.
	logicalOpts := opts
	logicalOpts.Policy = func(ctx context.Context, s Scope) error {
		cc, err := opts.Coordinates.Coordinate(c.Agent, s.Path)
		if err != nil || !coordinatePolicy(ctx, opts, policy, cc) {
			return errors.New("capturesource: policy denied")
		}
		return nil
	}
	q, code := classifyInfo(ctx, logicalOpts, policy, Root{c.Agent, c.Root}, logicalAdapter{adapters.All(opts.Home)[c.Agent]}, c.LogicalPath, info, observed)
	q.Path = c.Path
	q.LogicalPath = c.LogicalPath
	q.Identities = []string{c.LogicalPath, c.Path}
	return q, code
}

func absentCoordinate(ctx context.Context, opts Options, policy *capturepolicy.Policy, c capturecoordinates.Coordinate) (sessioncapture.Request, error) {
	fail := func(code string) (sessioncapture.Request, error) {
		return sessioncapture.Request{}, &ClassificationError{Issue{Root{c.Agent, c.Root}, c.Path, code}}
	}
	// coordinateInfo has checked route ancestors. Resolve must still reject any
	// undeclared identity, including links encountered before a missing component.
	if _, code := resolvedCoordinate(opts, c); code != "source_unreadable" {
		return fail("source_unreadable")
	}
	if _, err := capturepolicy.Resolve(c.Path); !errors.Is(err, os.ErrNotExist) {
		return fail("source_unreadable")
	}
	if !coordinatePolicy(ctx, opts, policy, c) {
		return fail("policy_denied")
	}
	sf, transform, code := classifySource(opts.Derived, Root{c.Agent, c.Root}, logicalAdapter{adapters.All(opts.Home)[c.Agent]}, c.LogicalPath)
	if code != "" {
		return fail(code)
	}
	if !nativeSessionSource(sf, transform) {
		return fail("native_session_layout_required")
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	q := emitRequest(opts, Root{c.Agent, c.Root}, sf, c.Path, now().UnixNano(), "")
	q.LogicalPath = c.LogicalPath
	q.Identities = []string{c.LogicalPath, c.Path}
	return q, nil
}

func coordinateAncestors(ctx context.Context, opts Options, policy *capturepolicy.Policy, c capturecoordinates.Coordinate, native bool) string {
	route := coordinateRoute(opts, c)
	if route == "" {
		return ""
	}
	rel, _ := filepath.Rel(route, c.Path)
	current := route
	parts := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for n, part := range parts {
		if ctx.Err() != nil {
			return "canceled"
		}
		current = filepath.Join(current, part)
		cc, e := opts.Coordinates.Coordinate(c.Agent, current)
		if e != nil || !coordinatePolicy(ctx, opts, policy, cc) {
			return "policy_denied"
		}
		code := coordinateAncestor(opts, cc, n == 0, n == len(parts)-1, native)
		if code != "" {
			return code
		}
	}
	return ""
}

func coordinateAncestor(opts Options, c capturecoordinates.Coordinate, root, last, native bool) string {
	st, err := os.Lstat(c.Path)
	if errors.Is(err, os.ErrNotExist) && native && !root {
		return ""
	}
	if err != nil {
		if root {
			return "root_unreadable"
		}
		return "source_unreadable"
	}
	if root {
		return coordinateDirectory(opts, c)
	}
	if !last && st.Mode()&os.ModeSymlink != 0 {
		return "alias_integration_required"
	}
	if !last && !st.IsDir() {
		return "nonregular_source"
	}
	return ""
}

func coordinateDirectory(opts Options, c capturecoordinates.Coordinate) string {
	actual, code := resolvedCoordinate(opts, c)
	if code != "" {
		return code
	}
	st, err := os.Stat(actual)
	if err != nil || !st.IsDir() {
		return "root_not_directory_or_alias"
	}
	return ""
}

type logicalAdapter struct{ adapters.Adapter }

func (a logicalAdapter) Classify(path, root string) (adapters.SessionFile, bool) {
	return adapters.ClassifyLogical(a.Adapter, path, root)
}

func coordinateIdentities(opts Options, c capturecoordinates.Coordinate) []string {
	ids := []string{c.LogicalPath, c.Path}
	for _, p := range opts.Coordinates.EquivalentPaths(c) {
		r, _ := capturepolicy.Resolve(p)
		ids = append(ids, p)
		ids = append(ids, r.Identities...)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// Prefer the caller's spelling when present; a missing logical hint may select
// an explicitly declared live route. No parent or sibling search is performed.
func availableCoordinate(opts Options, c capturecoordinates.Coordinate) capturecoordinates.Coordinate {
	if _, err := os.Lstat(c.Path); !errors.Is(err, os.ErrNotExist) {
		return c
	}
	for _, p := range opts.Coordinates.EquivalentPaths(c) {
		if _, err := os.Lstat(p); err == nil {
			got, e := opts.Coordinates.Coordinate(c.Agent, p)
			if e == nil {
				return got
			}
		}
	}
	return c
}
