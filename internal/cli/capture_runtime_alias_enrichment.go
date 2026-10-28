package cli

import (
	"errors"
	"os"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func (r runtimeComposition) enrichmentRootPaths(spec sessioncapture.EnrichmentSpec) ([]string, error) {
	if !runtimeHasAliases(r.config) {
		return []string{spec.Root}, nil
	}
	inventory, coordinate, err := r.aliasCoordinate(spec.Identity.Agent, spec.Root)
	if err != nil {
		return nil, err
	}
	return inventory.EquivalentPaths(coordinate), nil
}

func (r runtimeComposition) enrichmentObservedRoot(spec sessioncapture.EnrichmentSpec) (string, error) {
	if !runtimeHasAliases(r.config) {
		return spec.Root, nil
	}
	paths, err := r.enrichmentRootPaths(spec)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		if runtimeHasAliases(r.config) {
			if _, _, err := r.aliasCoordinate(spec.Identity.Agent, path); err != nil {
				continue
			}
		}
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("enrichment root unavailable")
}

func (r runtimeComposition) aliasEnrichmentExcluded(spec sessioncapture.EnrichmentSpec, registry exclusion.Registry) (bool, error) {
	if !runtimeHasAliases(r.config) {
		return false, nil
	}
	inventory, coordinate, err := r.aliasCoordinate(spec.Identity.Agent, spec.Root)
	if err != nil {
		return false, err
	}
	positions, err := inventory.PolicyCoordinates(coordinate)
	if err != nil {
		return false, err
	}
	for _, position := range positions {
		sf := adapters.SessionFile{Agent: spec.Identity.Agent, Root: position.Root, SessionKey: spec.AdapterSessionKey, Path: position.Path}
		if registry.Excludes(sf) {
			return true, nil
		}
		sf.SessionKey = ""
		if registry.Excludes(sf) {
			return true, nil
		}
	}
	return false, nil
}
