package capturecoordinates

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func inventory(t *testing.T, roots []Root, aliases []RootAlias, leaves []LeafAlias) *Inventory {
	t.Helper()
	i, err := Compile(roots, aliases, leaves)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func coordinate(t *testing.T, i *Inventory, agent, path string) Coordinate {
	t.Helper()
	c, err := i.Coordinate(agent, path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDefaultCustomAndNoFilesystem(t *testing.T) {
	roots := []Root{{"claude", "/home/absent/.claude"}, {"codex", "/custom/codex"}, {"pi", "/custom/pi"}}
	i := inventory(t, roots, nil, nil)
	for _, r := range roots {
		p := r.Path + "/sessions/file.jsonl"
		want := Coordinate{r.Agent, r.Path, p, p}
		if got := coordinate(t, i, r.Agent, p); got != want {
			t.Fatalf("coordinate mismatch: %#v", got)
		}
		if got := i.EquivalentPaths(want); !slices.Equal(got, []string{p}) {
			t.Fatal("unexpected equivalence")
		}
		pcs, err := i.PolicyCoordinates(want)
		if err != nil || !slices.Equal(pcs, []Coordinate{want}) {
			t.Fatal("default policy changed")
		}
	}
	for _, p := range []string{"relative", "/bad/../path", "/bad\x00path", "/bad\npath", "/bad\rpath", "/bad\xffpath", "/outside/file"} {
		if _, err := i.Coordinate("pi", p); err == nil {
			t.Fatal("accepted invalid path")
		}
	}
	if _, err := i.Coordinate("other", "/custom/pi/file"); err == nil {
		t.Fatal("accepted agent")
	}
}

func TestNestedProjectionAndOrder(t *testing.T) {
	roots := []Root{{"pi", "/logical"}, {"pi", "/logical/nested"}}
	aliases := []RootAlias{{"pi", "/logical", "/observed"}, {"pi", "/logical/nested", "/observed/nested"}}
	i := inventory(t, roots, aliases, nil)
	c := coordinate(t, i, "pi", "/observed/nested/file")
	if c.Root != "/logical/nested" || c.LogicalPath != "/logical/nested/file" {
		t.Fatal("parent stole nested identity")
	}
	slices.Reverse(roots)
	slices.Reverse(aliases)
	j := inventory(t, roots, aliases, nil)
	if coordinate(t, j, "pi", c.Path) != c || i.CursorFingerprint() != j.CursorFingerprint() {
		t.Fatal("order matters")
	}
	if !slices.Equal(i.EquivalentPaths(c), []string{"/logical/nested/file", "/observed/nested/file"}) {
		t.Fatal("wrong equivalents")
	}
	pcs, err := i.PolicyCoordinates(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []Coordinate{
		{"pi", "/logical/nested", "/logical/nested/file", "/logical/nested/file"},
		{"pi", "/observed/nested", "/observed/nested/file", "/observed/nested/file"},
		{"pi", "/observed", "/observed/nested/file", "/observed/nested/file"},
	} {
		if !slices.Contains(pcs, want) {
			t.Fatal("missing policy position")
		}
	}
	c.Root = "/forged"
	if _, err := i.PolicyCoordinates(c); err == nil || i.EquivalentPaths(c) != nil {
		t.Fatal("trusted request claims")
	}
}

func TestParentAliasOwnsNestedWithoutExplicitRoute(t *testing.T) {
	i := inventory(t, []Root{{"claude", "/logical"}, {"claude", "/logical/nested"}}, []RootAlias{{"claude", "/logical", "/physical"}}, nil)
	c := coordinate(t, i, "claude", "/physical/nested/x")
	if c.Root != "/logical/nested" {
		t.Fatal("incorrect nested owner")
	}
	pcs, err := i.PolicyCoordinates(c)
	if err != nil || !slices.Contains(pcs, Coordinate{"claude", "/physical/nested", "/physical/nested/x", "/physical/nested/x"}) {
		t.Fatal("missing inferred legacy nested root")
	}
}

func TestLeafExactAndSiblings(t *testing.T) {
	i := inventory(t, []Root{{"pi", "/root"}}, nil, []LeafAlias{{"pi", "/root/a", "/outside/alias"}, {"pi", "/root/b", "/root/old-b"}})
	c := coordinate(t, i, "pi", "/outside/alias")
	if c.LogicalPath != "/root/a" || !slices.Equal(i.EquivalentPaths(c), []string{"/outside/alias", "/root/a"}) {
		t.Fatal("leaf projection")
	}
	if _, err := i.Coordinate("pi", "/outside/sibling"); err == nil {
		t.Fatal("leaf authorized sibling")
	}
	if _, err := i.Coordinate("pi", "/outside/alias/child"); err == nil {
		t.Fatal("leaf authorized child")
	}
	sibling := coordinate(t, i, "pi", "/root/old-c")
	if sibling.LogicalPath != sibling.Path {
		t.Fatal("leaf changed sibling")
	}
	if coordinate(t, i, "pi", "/root/old-b").LogicalPath != "/root/b" {
		t.Fatal("leaf override lost")
	}
	if _, err := i.PolicyCoordinates(c); err != nil {
		t.Fatal(err)
	}
}

func TestRejectDeclarations(t *testing.T) {
	base := []Root{{"pi", "/logical"}, {"pi", "/other"}}
	cases := []struct {
		roots   []Root
		aliases []RootAlias
		leaves  []LeafAlias
	}{
		{roots: []Root{{"bad", "/logical"}}},
		{roots: []Root{{"pi", "relative"}}},
		{roots: []Root{{"pi", "/bad\nprivate"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/missing", "/physical"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/logical/missing", "/physical"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/logical", "/physical"}, {"pi", "/other", "/physical"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/logical", "/physical"}, {"pi", "/other", "/physical/nested"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/logical", "/other"}, {"pi", "/other", "/logical"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/logical", "/logical/loop"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/logical", "relative"}}},
		{roots: base, leaves: []LeafAlias{{"pi", "/missing/file", "/physical"}}},
		{roots: base, leaves: []LeafAlias{{"bad", "/logical/file", "/physical"}}},
		{roots: base, leaves: []LeafAlias{{"pi", "/logical/file", "relative"}}},
		{roots: base, leaves: []LeafAlias{{"pi", "/logical/file", "/logical"}}},
		{roots: base, leaves: []LeafAlias{{"pi", "/logical", "/physical"}}},
		{roots: base, leaves: []LeafAlias{{"pi", "/logical/a", "/physical"}, {"pi", "/logical/b", "/physical"}}},
		{roots: base, leaves: []LeafAlias{{"pi", "/logical/a", "/logical/b"}, {"pi", "/logical/b", "/logical/a"}}},
		{roots: base, aliases: []RootAlias{{"pi", "/logical", "/physical"}}, leaves: []LeafAlias{{"pi", "/physical/file", "/leaf"}}},
	}
	for n, tc := range cases {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			if _, err := Compile(tc.roots, tc.aliases, tc.leaves); err == nil {
				t.Fatal("accepted invalid declaration")
			} else if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "/logical") {
				t.Fatal("error leaked coordinate")
			}
		})
	}
}

func TestBoundsAndDepth(t *testing.T) {
	roots := make([]Root, MaxRoutes)
	for n := range roots {
		roots[n] = Root{"pi", fmt.Sprintf("/r%d", n)}
	}
	i := inventory(t, roots, nil, nil)
	if len(i.Routes()) != MaxRoutes {
		t.Fatal("route budget changed")
	}
	if _, err := Compile(roots, []RootAlias{{"pi", "/r0", "/alias"}}, nil); err == nil {
		t.Fatal("route budget not enforced")
	}
	leaves := make([]LeafAlias, MaxLeaves)
	for n := range leaves {
		leaves[n] = LeafAlias{"pi", fmt.Sprintf("/r0/f%d", n), fmt.Sprintf("/alias%d", n)}
	}
	inventory(t, roots, nil, leaves)
	if _, err := Compile(roots, nil, append(leaves, leaves[0])); err == nil {
		t.Fatal("leaf budget not enforced")
	}
	nested := []Root{}
	path := "/nested"
	for range MaxRoutes {
		nested = append(nested, Root{"pi", path})
		path += "/nested"
	}
	inventory(t, nested, nil, nil)
	aliasRoots := nested[:MaxDepth+1]
	aliases := make([]RootAlias, 0, len(aliasRoots))
	physical := "/physical"
	for _, r := range aliasRoots {
		aliases = append(aliases, RootAlias{"pi", r.Path, physical})
		physical += "/nested"
	}
	inventory(t, aliasRoots, aliases[:MaxDepth], nil)
	if _, err := Compile(aliasRoots, aliases, nil); err == nil {
		t.Fatal("alias depth not enforced")
	}
}

func TestFingerprintAndImmutability(t *testing.T) {
	roots := []Root{{"pi", "/root"}}
	aliases := []RootAlias{{"pi", "/root", "/alias"}}
	leaves := []LeafAlias{{"pi", "/root/file", "/leaf"}}
	i := inventory(t, roots, aliases, leaves)
	fingerprint := i.CursorFingerprint()
	if len(fingerprint) != 64 {
		t.Fatal("fingerprint shape")
	}
	variants := []*Inventory{
		inventory(t, []Root{{"pi", "/different"}}, nil, nil),
		inventory(t, roots, nil, leaves),
		inventory(t, roots, aliases, nil),
		inventory(t, roots, aliases, []LeafAlias{{"pi", "/root/other", "/leaf"}}),
		inventory(t, roots, []RootAlias{{"pi", "/root", "/other"}}, leaves),
	}
	for _, j := range variants {
		if j.CursorFingerprint() == fingerprint {
			t.Fatal("mapping mutation invisible")
		}
	}
	duplicate := inventory(t, append(roots, roots...), append(aliases, aliases...), append(leaves, leaves...))
	if duplicate.CursorFingerprint() != fingerprint {
		t.Fatal("duplicates changed mapping")
	}
	roots[0].Path = "/mutated"
	aliases[0].Path = "/mutated"
	leaves[0].Path = "/mutated"
	routes := i.Routes()
	routes[0].Path = "/mutated"
	c := coordinate(t, i, "pi", "/alias/file")
	paths := i.EquivalentPaths(c)
	paths[0] = "/mutated"
	pcs, err := i.PolicyCoordinates(c)
	if err != nil {
		t.Fatal(err)
	}
	pcs[0].Root = "/mutated"
	if i.CursorFingerprint() != fingerprint || len(i.EquivalentPaths(c)) != 3 {
		t.Fatal("mutable inventory")
	}
	var zero *Inventory
	if _, err := zero.Coordinate("pi", "/root/file"); err == nil {
		t.Fatal("nil inventory")
	}
	if zero.EquivalentPaths(c) != nil {
		t.Fatal("nil equivalence")
	}
	empty := inventory(t, nil, nil, nil)
	if _, err := empty.Coordinate("pi", "/file"); err == nil {
		t.Fatal("empty inventory admitted path")
	}
}
