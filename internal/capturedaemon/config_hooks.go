package capturedaemon

import (
	"errors"
	"os"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturepolicy"
)

// HookRoots translates only declared, validated root coordinates. Native hook
// management never follows an arbitrary symlink or infers transcript ownership.
// Multiple available, distinct directories for one logical root are ambiguous:
// activation/removal requires an explicit unambiguous installation coordinate.
func (c Config) HookRoots() ([]Root, error) {
	inventory, err := c.CoordinateInventory()
	if err != nil {
		return nil, err
	}
	roots := c.SourceRoots()
	out := make([]Root, 0, len(roots))
	for _, root := range roots {
		path, err := c.hookRootPath(inventory, root)
		if err != nil {
			return nil, err
		}
		out = append(out, Root{Agent: root.Agent, Path: path})
	}
	return out, nil
}

func (c Config) hookRootPath(inventory *capturecoordinates.Inventory, root Root) (string, error) {
	coordinate, err := inventory.Coordinate(root.Agent, root.Path)
	if err != nil || coordinate.Root != root.Path || coordinate.LogicalPath != root.Path {
		return "", errors.New("invalid hook root coordinate")
	}
	physical := ""
	aliased := false
	// A compiled leaf cannot name a root. Parent-root routes project nested roots.
	for _, path := range inventory.EquivalentPaths(coordinate) {
		aliased = aliased || path != root.Path
		resolved, err := c.hookRoute(inventory, root.Agent, path)
		if err != nil {
			return "", err
		}
		if resolved == "" {
			continue
		}
		if physical != "" && physical != resolved {
			return "", errors.New("hook root has ambiguous physical coordinates")
		}
		physical = resolved
	}
	if physical != "" {
		return physical, nil
	}
	if aliased {
		return "", errors.New("declared hook root unavailable")
	}
	return root.Path, SafePath(root.Path)
}

func (c Config) hookRoute(inventory *capturecoordinates.Inventory, agent, path string) (string, error) {
	if err := c.validateAliasPath(inventory, agent, path); err != nil {
		return "", err
	}
	resolved, err := capturepolicy.Resolve(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("hook root unavailable")
	}
	if err := SafePath(resolved.Path); err != nil {
		return "", err
	}
	st, err := os.Stat(resolved.Path)
	if err != nil || !st.IsDir() {
		return "", errors.New("hook root is not a directory")
	}
	return resolved.Path, nil
}
