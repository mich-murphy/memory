package cli

import (
	"fmt"
	"os"
	"runtime/coverage"
)

// flushFixtureCoverage is only for the explicit fixture crash boundary. It does
// not consult GOCOVERDIR: normal commands and uninstrumented fixtures have no
// coverage dependency. The gate owns this directory and merges it with unit data.
func flushFixtureCoverage() error {
	dir := os.Getenv("E2E_GOCOVERDIR")
	if dir == "" {
		return nil
	}
	if err := writeFixtureCoverageMetadata(dir); err != nil {
		return err
	}
	// WriteCountersDir requires atomic instrumentation. Do not silently accept
	// ordinary test coverage or a failed write as a successful fixture crash.
	if err := coverage.WriteCountersDir(dir); err != nil {
		return fmt.Errorf("fixture coverage counters (atomic instrumentation required): %w", err)
	}
	return nil
}

// writeFixtureCoverageMetadata checks the actual runtime API before crash-stage
// side effects and again at the final flush. It never creates the gate-owned dir.
func writeFixtureCoverageMetadata(dir string) error {
	if dir == "" {
		return nil
	}
	if err := coverage.WriteMetaDir(dir); err != nil {
		return fmt.Errorf("fixture coverage metadata: %w", err)
	}
	return nil
}
