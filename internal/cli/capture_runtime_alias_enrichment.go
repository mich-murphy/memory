package cli

import (
	"errors"
	"os"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func runtimeEnrichmentRootPaths(c capturedaemon.Config, spec sessioncapture.EnrichmentSpec) ([]string, error) {
	if !runtimeHasAliases(c) {
		return []string{spec.Root}, nil
	}
	inventory, coordinate, err := runtimeAliasCoordinate(c, spec.Identity.Agent, spec.Root)
	if err != nil {
		return nil, err
	}
	return inventory.EquivalentPaths(coordinate), nil
}

func runtimeEnrichmentObservedRoot(c capturedaemon.Config, spec sessioncapture.EnrichmentSpec) (string, error) {
	if !runtimeHasAliases(c) {
		return spec.Root, nil
	}
	paths, err := runtimeEnrichmentRootPaths(c, spec)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		if runtimeHasAliases(c) {
			if _, _, err := runtimeAliasCoordinate(c, spec.Identity.Agent, path); err != nil {
				continue
			}
		}
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("enrichment root unavailable")
}

func runtimeAliasEnrichmentExcluded(c capturedaemon.Config, spec sessioncapture.EnrichmentSpec, registry exclusion.Registry) (bool, error) {
	if !runtimeHasAliases(c) {
		return false, nil
	}
	inventory, coordinate, err := runtimeAliasCoordinate(c, spec.Identity.Agent, spec.Root)
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
