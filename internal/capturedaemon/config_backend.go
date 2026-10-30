package capturedaemon

import (
	"errors"

	"github.com/mich-murphy/memory/internal/capturebackend"
)

// BackendSpec preserves the existing store-only local configuration contract.
func (c Config) BackendSpec() capturebackend.Spec {
	if c.Backend != nil {
		return *c.Backend
	}
	return capturebackend.Spec{Kind: "local", LocalDir: c.Store}
}

// LocalProtectedPaths is the actual local storage/policy footprint, never an
// empty remote-store placeholder (which would spuriously overlap every path).
func (c Config) LocalProtectedPaths() []string {
	paths := make([]string, 0, 3)
	for _, p := range []string{c.Spool, c.PolicyDir} {
		if p != "" {
			paths = append(paths, p)
		}
	}
	spec := c.BackendSpec()
	if spec.Kind == "local" && spec.LocalDir != "" {
		paths = append(paths, spec.LocalDir)
	}
	return paths
}

func (c Config) validateBackend() error {
	if c.Backend != nil && c.Store != "" {
		return errors.New("capture store and backend are mutually exclusive")
	}
	return c.BackendSpec().Validate()
}
