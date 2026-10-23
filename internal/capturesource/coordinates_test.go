package capturesource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
)

func inventory(t *testing.T, roots []Root, aliases []capturecoordinates.RootAlias, leaves []capturecoordinates.LeafAlias) *capturecoordinates.Inventory {
	t.Helper()
	rr := make([]capturecoordinates.Root, len(roots))
	for i, r := range roots {
		rr[i] = capturecoordinates.Root(r)
	}
	inv, err := capturecoordinates.Compile(rr, aliases, leaves)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestCoordinateAdapterParity(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "pi"} {
		t.Run(agent, func(t *testing.T) {
			o := coordinateOptions(t)
			o.OnlyRoots = true
			root := filepath.Join(o.Home, "custom-"+agent)
			physical := filepath.Join(o.Home, "moved-"+agent)
			o.Roots = []Root{{agent, root}}
			rels := []string{"projects/project/s.jsonl", "projects/project/s/subagents/child.jsonl"}
			if agent == "codex" {
				rels = []string{"sessions/2026/rollout-2026-01-01T00-00-00-11111111-1111-1111-1111-111111111111.jsonl"}
			}
			if agent == "pi" {
				rels = []string{"sessions/--project--/session.jsonl"}
			}
			for _, rel := range rels {
				fixture(t, filepath.Join(root, rel), "synthetic raw")
			}
			baseline := discover(t, o)
			if err := os.Rename(root, physical); err != nil {
				t.Fatal(err)
			}
			o.Coordinates = inventory(t, o.Roots, []capturecoordinates.RootAlias{{Agent: agent, Root: root, Path: physical}}, nil)
			got := discover(t, o)
			if len(got.Requests) != len(baseline.Requests) {
				t.Fatalf("requests %d != %d; %+v", len(got.Requests), len(baseline.Requests), got.Issues)
			}
			for _, q := range baseline.Requests {
				observed := filepath.Join(physical, relativeKey(root, q.Path))
				alias, err := Classify(t.Context(), o, agent, observed)
				if err != nil {
					t.Fatal(err)
				}
				if alias.Path != observed || alias.Identity != q.Identity || alias.SourceID != q.SourceID || alias.Role != q.Role || alias.Optional != q.Optional || !slices.Equal(alias.Associations, q.Associations) {
					t.Fatalf("identity drift: %+v != %+v", alias, q)
				}
				native, err := ClassifyNative(t.Context(), o, agent, observed)
				if err != nil || native.Identity != q.Identity {
					t.Fatalf("native drift: %+v %v", native, err)
				}
			}
		})
	}
}

func TestCoordinateExactLeafAndAbsentNative(t *testing.T) {
	o := coordinateOptions(t)
	o.OnlyRoots = true
	root := filepath.Join(o.Home, "logical")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	logical := filepath.Join(root, "projects", "p", "s.jsonl")
	observed := filepath.Join(o.Home, "outside", "arbitrary-name.bin")
	fixture(t, filepath.Join(o.Home, "outside", "sibling.jsonl"), "not authorized")
	o.Roots = []Root{{"claude", root}}
	o.Coordinates = inventory(t, o.Roots, nil, []capturecoordinates.LeafAlias{{Agent: "claude", LogicalPath: logical, Path: observed}})
	before, err := ClassifyNative(t.Context(), o, "claude", observed)
	if err != nil {
		t.Fatal(err)
	}
	fixture(t, observed, "not json")
	after, err := Classify(t.Context(), o, "claude", observed)
	if err != nil {
		t.Fatal(err)
	}
	if before.Identity != after.Identity || before.SourceID != after.SourceID {
		t.Fatal("absent native drift")
	}
	got := discover(t, o)
	if len(got.Requests) != 1 || got.Requests[0].Path != observed {
		t.Fatalf("%+v", got)
	}
	for _, p := range []string{filepath.Dir(observed), filepath.Join(filepath.Dir(observed), "sibling.jsonl")} {
		if _, err := Classify(t.Context(), o, "claude", p); err == nil {
			t.Fatal("leaf authorized parent/sibling")
		}
	}
}

func TestCoordinateRootLinkAndUndeclaredDescendant(t *testing.T) {
	o := coordinateOptions(t)
	o.OnlyRoots = true
	root := filepath.Join(o.Home, "logical")
	physical := filepath.Join(o.Home, "physical")
	fixture(t, filepath.Join(physical, "projects", "p", "s.jsonl"), "raw")
	if err := os.Symlink(physical, root); err != nil {
		t.Fatal(err)
	}
	o.Roots = []Root{{"claude", root}}
	o.Coordinates = inventory(t, o.Roots, []capturecoordinates.RootAlias{{Agent: "claude", Root: root, Path: physical}}, nil)
	q, err := Classify(t.Context(), o, "claude", filepath.Join(root, "projects", "p", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Path != filepath.Join(root, "projects", "p", "s.jsonl") {
		t.Fatal("lost original path")
	}
	external := filepath.Join(o.Home, "unmapped")
	fixture(t, filepath.Join(external, "bad.jsonl"), "raw")
	if err := os.Symlink(external, filepath.Join(physical, "projects", "escape")); err != nil {
		t.Fatal(err)
	}
	got := discover(t, o)
	if !hasCode(got, "alias_integration_required") {
		t.Fatalf("%+v", got.Issues)
	}
	for _, q := range got.Requests {
		if filepath.Base(q.Path) == "bad.jsonl" {
			t.Fatal("followed undeclared directory")
		}
	}
	// A chain through an undeclared identity must fail despite its declared final target.
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	middle := filepath.Join(o.Home, "middle")
	if err := os.Symlink(physical, middle); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(middle, root); err != nil {
		t.Fatal(err)
	}
	if _, err := Classify(t.Context(), o, "claude", filepath.Join(root, "projects", "p", "s.jsonl")); err == nil {
		t.Fatal("undeclared intermediate identity admitted")
	}
}

func TestCoordinateNestedOwnershipAfterProjection(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(strconv.FormatBool(reverse), func(t *testing.T) {
			o := coordinateOptions(t)
			o.OnlyRoots = true
			parent := filepath.Join(o.Home, "logical")
			nested := filepath.Join(parent, "nested")
			physical := filepath.Join(o.Home, "physical")
			path := fixture(t, filepath.Join(physical, "nested", "projects", "p", "s.jsonl"), "raw")
			o.Roots = []Root{{"claude", parent}, {"claude", nested}}
			if reverse {
				slices.Reverse(o.Roots)
			}
			o.Coordinates = inventory(t, o.Roots, []capturecoordinates.RootAlias{{Agent: "claude", Root: parent, Path: physical}}, nil)
			got := discover(t, o)
			if len(got.Requests) != 1 {
				t.Fatalf("requests=%d issues=%+v", len(got.Requests), got.Issues)
			}
			q, err := Classify(t.Context(), o, "claude", path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Requests[0].Identity != q.Identity || !slices.Contains(q.Associations, "root:"+nested) {
				t.Fatal("nested identity lost")
			}
		})
	}
}

func TestCoordinatePolicyUnionAndMissingRoot(t *testing.T) {
	o := coordinateOptions(t)
	o.OnlyRoots = true
	root := filepath.Join(o.Home, "logical")
	physical := filepath.Join(o.Home, "physical")
	path := fixture(t, filepath.Join(physical, "projects", "p", "s.jsonl"), "raw")
	logical := filepath.Join(root, "projects", "p", "s.jsonl")
	o.Roots = []Root{{"claude", root}}
	o.Coordinates = inventory(t, o.Roots, []capturecoordinates.RootAlias{{Agent: "claude", Root: root, Path: physical}}, nil)
	seen := false
	o.Policy = func(_ context.Context, s Scope) error {
		if slices.Contains(s.Identities, logical) && slices.Contains(s.Identities, path) {
			seen = true
			return errors.New("private")
		}
		return nil
	}
	if _, err := Classify(t.Context(), o, "claude", path); err == nil || !seen {
		t.Fatal("missing union policy")
	}
	o.Policy = func(context.Context, Scope) error { return nil }
	if q, err := ClassifyNative(t.Context(), o, "claude", logical); err != nil || q.Path != path {
		t.Fatalf("live declared fallback not selected: %v", err)
	}
	// The healthy observed route admits a missing descendant, without changing IDs.
	missing := filepath.Join(physical, "projects", "p", "later.jsonl")
	native, err := ClassifyNative(t.Context(), o, "claude", missing)
	if err != nil {
		t.Fatal(err)
	}
	fixture(t, missing, "later")
	raw, err := Classify(t.Context(), o, "claude", missing)
	if err != nil || native.Identity != raw.Identity || native.SourceID != raw.SourceID {
		t.Fatalf("native drift %v", err)
	}
}

func TestCoordinateCursorReplacementAndCancellation(t *testing.T) {
	o := coordinateOptions(t)
	o.OnlyRoots = true
	o.EntriesPerRoot = 1
	root := filepath.Join(o.Home, "root")
	fixture(t, filepath.Join(root, "history.jsonl"), "raw")
	o.Roots = []Root{{"codex", root}}
	o.Coordinates = inventory(t, o.Roots, nil, nil)
	got := discover(t, o)
	o.Cursor = got.Cursor
	moved := root + "-old"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	fixture(t, filepath.Join(root, "other.jsonl"), "new")
	next := discover(t, o)
	if !hasCode(next, "root_replaced") {
		t.Fatalf("%+v", next)
	}
	o.Coordinates = inventory(t, o.Roots, []capturecoordinates.RootAlias{{Agent: "codex", Root: root, Path: moved}}, nil)
	if _, err := Discover(t.Context(), o); err == nil {
		t.Fatal("mapping change retained cursor")
	}
	o.Cursor = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Discover(ctx, o); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func coordinateOptions(t *testing.T) Options {
	t.Helper()
	o := options(t)
	home, err := filepath.EvalSymlinks(o.Home)
	if err != nil {
		t.Fatal(err)
	}
	o.Home = home
	return o
}
