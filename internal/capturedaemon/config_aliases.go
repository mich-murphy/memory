package capturedaemon

import (
	"errors"
	"os"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturepolicy"
)

// Declarations preserve the existing logical root namespace. They are not
// permission to follow arbitrary directory links below an admitted entry point.
type RootAlias struct {
	Agent string `json:"agent"`
	Root  string `json:"root"`
	Path  string `json:"path"`
}
type LeafAlias struct {
	Agent       string `json:"agent"`
	LogicalPath string `json:"logical_path"`
	Path        string `json:"path"`
}

func (c Config) CoordinateInventory() (*capturecoordinates.Inventory, error) {
	roots := c.SourceRoots()
	mapped := make([]capturecoordinates.Root, len(roots))
	for i, r := range roots {
		mapped[i] = capturecoordinates.Root{Agent: r.Agent, Path: r.Path}
	}
	aliases := make([]capturecoordinates.RootAlias, len(c.RootAliases))
	for i, r := range c.RootAliases {
		aliases[i] = capturecoordinates.RootAlias{Agent: r.Agent, Root: r.Root, Path: r.Path}
	}
	leaves := make([]capturecoordinates.LeafAlias, len(c.LeafAliases))
	for i, r := range c.LeafAliases {
		leaves[i] = capturecoordinates.LeafAlias{Agent: r.Agent, LogicalPath: r.LogicalPath, Path: r.Path}
	}
	return capturecoordinates.Compile(mapped, aliases, leaves)
}

func (c Config) validateAliases() error {
	inventory, err := c.CoordinateInventory()
	if err != nil {
		return err
	}
	for _, route := range inventory.Routes() {
		if err := c.validateAliasPath(inventory, route.Agent, route.Path); err != nil {
			return err
		}
	}
	for _, leaf := range inventory.Leaves() {
		for _, path := range []string{leaf.Path, leaf.LogicalPath} {
			if err := c.validateAliasPath(inventory, leaf.Agent, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c Config) validateAliasPath(inventory *capturecoordinates.Inventory, agent, path string) error {
	coordinate, err := inventory.Coordinate(agent, path)
	if err != nil {
		return err
	}
	resolved, err := capturepolicy.Resolve(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("capture source alias unavailable")
	}
	for _, identity := range resolved.Identities {
		position, err := inventory.Coordinate(agent, identity)
		if err != nil || position.Root != coordinate.Root || position.LogicalPath != coordinate.LogicalPath {
			return errors.New("capture source alias has undeclared resolution")
		}
		if capturepolicy.IsCredential(identity) {
			return errors.New("capture source alias is protected")
		}
		for _, protected := range c.LocalProtectedPaths() {
			if Overlap(identity, protected) {
				return errors.New("capture source alias overlaps storage or policy")
			}
		}
	}
	return nil
}
