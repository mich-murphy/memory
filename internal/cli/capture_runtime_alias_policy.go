package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func runtimeHasAliases(c capturedaemon.Config) bool {
	return len(c.RootAliases) != 0 || len(c.LeafAliases) != 0
}

func (r runtimeComposition) aliasMapped(agent, path string) bool {
	if !runtimeHasAliases(r.config) {
		return false
	}
	inventory := r.inventory
	if inventory == nil {
		return false
	}
	_, err := inventory.Coordinate(agent, path)
	return err == nil
}

func (r runtimeComposition) aliasCoordinate(agent, path string) (*capturecoordinates.Inventory, capturecoordinates.Coordinate, error) {
	inventory := r.inventory
	if inventory == nil {
		return nil, capturecoordinates.Coordinate{}, errors.New("coordinate inventory unavailable")
	}
	coordinate, err := inventory.Coordinate(agent, path)
	if err != nil {
		return nil, coordinate, err
	}
	resolution, err := capturepolicy.Resolve(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, coordinate, errors.New("source alias resolution unavailable")
	}
	for _, identity := range resolution.Identities {
		resolved, err := inventory.Coordinate(agent, identity)
		if err != nil || resolved.Root != coordinate.Root || resolved.LogicalPath != coordinate.LogicalPath {
			return nil, coordinate, errors.New("source alias resolution is undeclared")
		}
	}
	return inventory, coordinate, nil
}

func (r runtimeComposition) aliasPrivacyOwner(ctx context.Context, req sessioncapture.Request, discovery bool) (*adapters.SessionFile, error) {
	inventory, coordinate, err := r.aliasCoordinate(req.Identity.Agent, req.Path)
	if err != nil {
		return nil, err
	}
	directory, err := runtimeAliasDirectory(inventory, coordinate, req)
	if err != nil || directory {
		return nil, err
	}
	rel, _ := filepath.Rel(coordinate.Root, coordinate.LogicalPath)
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if req.Identity.Agent == "claude" && runtimeExternalScope(first) {
		return runtimeExternalPrivacyOwner(ctx, r.config, req, coordinate.Root, rel, discovery)
	}
	adapter := adapters.All(r.config.Home)[req.Identity.Agent]
	if adapter == nil {
		return nil, errors.New("source agent unsupported")
	}
	sf, ok := adapters.ClassifyLogical(adapter, coordinate.LogicalPath, coordinate.Root)
	if !ok {
		sf = adapters.SessionFile{Agent: req.Identity.Agent, Root: coordinate.Root, Path: coordinate.LogicalPath}
	}
	sf.SourcePath = req.Path
	return &sf, nil
}

// runtimeAliasDirectory distinguishes metadata traversal from aggregate artifacts.
// A missing logical directory may have a declared available physical spelling.
func runtimeAliasDirectory(inventory *capturecoordinates.Inventory, coordinate capturecoordinates.Coordinate, req sessioncapture.Request) (bool, error) {
	st, err := os.Stat(req.Path)
	if err == nil && st.IsDir() && req.Role == "" {
		return true, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, errors.New("source metadata unavailable")
	}
	if err == nil && !st.Mode().IsRegular() {
		return false, errors.New("source is not a regular artifact")
	}
	if !errors.Is(err, os.ErrNotExist) || req.Role != "" {
		return false, nil
	}
	if coordinate.LogicalPath == coordinate.Root {
		return true, nil
	}
	for _, path := range inventory.EquivalentPaths(coordinate) {
		if st, e := os.Stat(path); e == nil && st.IsDir() {
			return true, nil
		}
	}
	return false, nil
}

// An old opt-out can name a now-absent physical root. The compiled declaration,
// not current inode coincidence or request metadata, supplies every equivalent
// legacy root/session coordinate checked against the preserved registry.
func (r runtimeComposition) aliasExcluded(req sessioncapture.Request, owner adapters.SessionFile, registry exclusion.Registry) (bool, error) {
	inventory, coordinate, err := r.aliasCoordinate(req.Identity.Agent, req.Path)
	if err != nil {
		return false, err
	}
	positions, err := inventory.PolicyCoordinates(coordinate)
	if err != nil {
		return false, err
	}
	adapter := adapters.All(r.config.Home)[req.Identity.Agent]
	if adapter == nil {
		return false, errors.New("source agent unsupported")
	}
	for _, position := range positions {
		// Ownership was independently validated before this call (including the
		// bounded durable index for external artifacts). A second classification
		// would both scan unrelated projects and possibly choose a different slug.
		sf := owner
		sf.SessionKey = runtimeClaimedLegacyKey(adapter, owner.Root, position.Root, owner.SessionKey)
		sf.Root = position.Root
		sf.Path = position.LogicalPath
		sf.SourcePath = position.Path
		if registry.Excludes(sf) {
			return true, nil
		}
	}
	return false, nil
}
