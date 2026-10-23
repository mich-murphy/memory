// Package capturecoordinates compiles declared path equivalence without filesystem
// access. Declarations are authority, not proof that any path is safe to open.
// Callers must classify LogicalPath relative to Root, and independently validate
// every physical open (including root-entry aliases and descendant symlinks).
package capturecoordinates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	MaxRoutes = 128
	MaxLeaves = 4096
	// MaxDepth bounds nesting of declared alias routes only. Canonical roots and
	// source pathname component counts do not consume this alias budget.
	MaxDepth = 8
)

type (
	Root       struct{ Agent, Path string }
	RootAlias  struct{ Agent, Root, Path string }
	LeafAlias  struct{ Agent, LogicalPath, Path string }
	RootRoute  struct{ Agent, Root, Path string }
	Coordinate struct{ Agent, Root, LogicalPath, Path string }
)

// Inventory is immutable after Compile and safe for concurrent readers.
type Inventory struct {
	roots       []Root
	routes      []RootRoute
	leaves      []LeafAlias
	fingerprint string
}

var (
	errDeclaration = errors.New("capturecoordinates: invalid declaration")
	errCoordinate  = errors.New("capturecoordinates: invalid coordinate")
)

func validAgent(a string) bool { return a == "claude" || a == "codex" || a == "pi" }
func validPath(p string) bool {
	return utf8.ValidString(p) && !strings.ContainsAny(p, "\x00\r\n") && filepath.IsAbs(p) && filepath.Clean(p) == p
}

func beneath(root, path string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

func project(root, target, path string) string {
	rel, _ := filepath.Rel(root, path)
	return filepath.Join(target, rel)
}

func routeLess(a, b RootRoute) int {
	return strings.Compare(a.Agent+"\x00"+a.Path+"\x00"+a.Root, b.Agent+"\x00"+b.Path+"\x00"+b.Root)
}

// Compile accepts only direct aliases to existing logical roots/files. It rejects
// ambiguous directory overlaps even when a longest-physical-prefix rule could
// choose one: declarations must agree, rather than silently steal descendants.
// Duplicate declarations consume budget but are otherwise idempotent.
func Compile(roots []Root, aliases []RootAlias, leaves []LeafAlias) (*Inventory, error) {
	if len(roots)+len(aliases) > MaxRoutes || len(leaves) > MaxLeaves {
		return nil, errDeclaration
	}
	i := &Inventory{roots: slices.Clone(roots), leaves: slices.Clone(leaves)}
	for _, r := range roots {
		if !validAgent(r.Agent) || !validPath(r.Path) {
			return nil, errDeclaration
		}
		i.routes = append(i.routes, RootRoute{r.Agent, r.Path, r.Path})
	}
	for _, a := range aliases {
		if !validAgent(a.Agent) || !validPath(a.Root) || !validPath(a.Path) || i.owner(a.Agent, a.Root) != a.Root {
			return nil, errDeclaration
		}
		// A containing root is insufficient: Root is an existing stable identity.
		if !slices.Contains(roots, Root{a.Agent, a.Root}) {
			return nil, errDeclaration
		}
		i.routes = append(i.routes, RootRoute(a))
	}
	slices.SortFunc(i.routes, routeLess)
	i.routes = slices.Compact(i.routes)
	if !i.validRoutes() {
		return nil, errDeclaration
	}
	if !i.compileLeaves() {
		return nil, errDeclaration
	}
	i.setFingerprint()
	return i, nil
}

func (i *Inventory) compileLeaves() bool {
	byPath := make(map[Root]string, len(i.leaves))
	for _, l := range i.leaves {
		if !i.validLeaf(l) {
			return false
		}
		key := Root{l.Agent, l.Path}
		if prior, ok := byPath[key]; ok && prior != l.LogicalPath {
			return false
		}
		byPath[key] = l.LogicalPath
	}
	// Canonical leaves may not themselves redirect: reject chains/cycles.
	for _, l := range i.leaves {
		if target, ok := byPath[Root{l.Agent, l.LogicalPath}]; ok && target != l.LogicalPath {
			return false
		}
	}
	slices.SortFunc(i.leaves, func(a, b LeafAlias) int {
		return strings.Compare(a.Agent+"\x00"+a.Path+"\x00"+a.LogicalPath, b.Agent+"\x00"+b.Path+"\x00"+b.LogicalPath)
	})
	i.leaves = slices.Compact(i.leaves)
	return true
}

func (i *Inventory) owner(agent, path string) string {
	best := ""
	for _, r := range i.roots {
		if r.Agent == agent && beneath(r.Path, path) && len(r.Path) > len(best) {
			best = r.Path
		}
	}
	return best
}

func (i *Inventory) validRoutes() bool {
	for _, a := range i.routes {
		depth := 0
		for _, b := range i.routes {
			if a.Agent != b.Agent || !beneath(b.Path, a.Path) {
				continue
			}
			if b.Path != b.Root {
				depth++
			}
			if project(b.Path, b.Root, a.Path) != a.Root {
				return false
			}
		}
		if depth > MaxDepth {
			return false
		}
	}
	return true
}

func (i *Inventory) directory(agent, path string) string {
	for _, r := range i.routes {
		if r.Agent == agent && beneath(r.Path, path) {
			return project(r.Path, r.Root, path)
		}
	}
	return ""
}

func (i *Inventory) validLeaf(l LeafAlias) bool {
	if !validAgent(l.Agent) || !validPath(l.Path) || !validPath(l.LogicalPath) || i.owner(l.Agent, l.LogicalPath) == "" {
		return false
	}
	// Logical coordinates must not already be physical directory aliases.
	if p := i.directory(l.Agent, l.LogicalPath); p != l.LogicalPath {
		return false
	}
	// A leaf must never replace a directory route entry or a logical root.
	for _, r := range i.routes {
		if r.Agent == l.Agent && (beneath(l.Path, r.Path) || l.LogicalPath == r.Root) {
			return false
		}
	}
	return true
}

// Coordinate projects only declared root prefixes and exact leaf aliases. Root
// ownership is selected AFTER projection, preserving nested-root identities.
func (i *Inventory) Coordinate(agent, path string) (Coordinate, error) {
	if i == nil || !validAgent(agent) || !validPath(path) {
		return Coordinate{}, errCoordinate
	}
	logical := i.directory(agent, path)
	for _, l := range i.leaves {
		if l.Agent == agent && l.Path == path {
			logical = l.LogicalPath
			break
		}
	}
	root := i.owner(agent, logical)
	if root == "" {
		return Coordinate{}, errCoordinate
	}
	return Coordinate{agent, root, logical, path}, nil
}

func (i *Inventory) verified(c Coordinate) bool {
	got, err := i.Coordinate(c.Agent, c.Path)
	return err == nil && got == c
}

// EquivalentPaths returns sorted exact declared spellings of a verified file.
// Leaf aliases confer no authority over siblings or descendants.
func (i *Inventory) EquivalentPaths(c Coordinate) []string {
	if !i.verified(c) {
		return nil
	}
	paths := []string{c.LogicalPath}
	for _, r := range i.routes {
		if r.Agent == c.Agent && beneath(r.Root, c.LogicalPath) {
			p := project(r.Root, r.Path, c.LogicalPath)
			got, err := i.Coordinate(c.Agent, p)
			if err == nil && got.LogicalPath == c.LogicalPath && got.Root == c.Root {
				paths = append(paths, p)
			}
		}
	}
	for _, l := range i.leaves {
		if l.Agent == c.Agent && l.LogicalPath == c.LogicalPath {
			paths = append(paths, l.Path)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// PolicyCoordinates includes legacy physical-root namespaces for SessionHash
// checks. These are policy checks ONLY, never classification/identity inputs.
// Request-supplied Root/LogicalPath values are checked against this inventory.
func (i *Inventory) PolicyCoordinates(c Coordinate) ([]Coordinate, error) {
	if !i.verified(c) {
		return nil, errCoordinate
	}
	out := []Coordinate{{c.Agent, c.Root, c.LogicalPath, c.LogicalPath}}
	for _, p := range i.EquivalentPaths(c) {
		out = append(out, Coordinate{c.Agent, c.Root, c.LogicalPath, p})
		for _, r := range i.routes {
			if r.Agent == c.Agent && beneath(r.Path, p) {
				out = append(out, Coordinate{c.Agent, r.Path, p, p})
				if beneath(r.Root, c.Root) {
					out = append(out, Coordinate{c.Agent, project(r.Root, r.Path, c.Root), p, p})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b Coordinate) int {
		return strings.Compare(a.Agent+"\x00"+a.Root+"\x00"+a.LogicalPath+"\x00"+a.Path, b.Agent+"\x00"+b.Root+"\x00"+b.LogicalPath+"\x00"+b.Path)
	})
	return slices.Compact(out), nil
}

// Leaves returns only declared exact-file routes, never their parent authority.
func (i *Inventory) Leaves() []LeafAlias { return slices.Clone(i.leaves) }

func (i *Inventory) Routes() []RootRoute       { return slices.Clone(i.routes) }
func (i *Inventory) CursorFingerprint() string { return i.fingerprint }
func (i *Inventory) setFingerprint() {
	roots := slices.Clone(i.roots)
	slices.SortFunc(roots, func(a, b Root) int { return strings.Compare(a.Agent+"\x00"+a.Path, b.Agent+"\x00"+b.Path) })
	roots = slices.Compact(roots)
	// Fixed structs and sorted slices make the encoding deterministic. These types
	// contain strings only, so JSON marshaling cannot fail.
	data, _ := json.Marshal(struct {
		Version int
		Roots   []Root
		Routes  []RootRoute
		Leaves  []LeafAlias
	}{1, roots, i.routes, i.leaves})
	sum := sha256.Sum256(data)
	i.fingerprint = hex.EncodeToString(sum[:])
}
