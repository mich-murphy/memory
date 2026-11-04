package cli

import (
	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
)

// runtimeComposition owns one immutable declaration snapshot for this invocation.
// Live filesystem, registry and native policy decisions are never stored here.
type runtimeComposition struct {
	config    capturedaemon.Config
	inventory *capturecoordinates.Inventory
	// Optional counting seams for tests; nil selects the production loaders.
	registryLoader func(string) (exclusion.Registry, error)
	filesystemSet  func(capturedaemon.Config) (*capturepolicy.Policy, error)
}

func newRuntimeComposition(c capturedaemon.Config) (runtimeComposition, error) {
	return composeRuntime(c, c.CoordinateInventory)
}

// compile is injectable to count compilation without global hooks or races.
func composeRuntime(c capturedaemon.Config, compile func() (*capturecoordinates.Inventory, error)) (runtimeComposition, error) {
	inventory, err := compile()
	if err != nil {
		return runtimeComposition{}, err
	}
	return runtimeComposition{config: c, inventory: inventory}, nil
}
