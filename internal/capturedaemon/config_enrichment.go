package capturedaemon

import (
	"errors"
	"unicode/utf8"
)

// Extra roots authorize only trusted snapshot selectors, never recursive source
// discovery. Explicit inventory avoids pulling host managed/ancestor files into a
// synthetic Home or silently broadening ordinary source admission.
func (c Config) validateEnrichmentRoots() error {
	if len(c.SourceRoots())+len(c.RootAliases)+len(c.EnrichmentRoots) > 128 {
		return errors.New("combined capture/enrichment inventory exceeds root limit")
	}
	for _, root := range c.EnrichmentRoots {
		if !utf8.ValidString(root) || len(root) > 4096 {
			return errors.New("invalid enrichment root")
		}
		if err := SafePath(root); err != nil {
			return err
		}
		for _, protected := range c.LocalProtectedPaths() {
			if Overlap(root, protected) {
				return errors.New("enrichment root overlaps storage or policy")
			}
		}
	}
	return nil
}
