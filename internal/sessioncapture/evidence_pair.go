package sessioncapture

import (
	"fmt"

	"github.com/mich-murphy/memory/internal/publication"
)

// validateEvidencePair guards the private positional join at its durable boundary.
// The public manifest remains the only publication model; evidence is local policy
// provenance, including the original descriptor for an optional withheld source.
func validateEvidencePair(m publication.Manifest, es []evidence, allowHistoricalUnbound bool) error {
	if len(m.Sources) != len(es) {
		return fmt.Errorf("%w: evidence count", ErrIntegrity)
	}
	for i := range m.Sources {
		public := m.Sources[i]
		e := es[i]
		identity := public
		if public.Withheld != nil {
			var err error
			identity, err = rehydrateSource(public, e)
			if err != nil {
				return fmt.Errorf("%w: evidence commitment", ErrIntegrity)
			}
		}
		if e.EpochID != identity.EpochID && (!allowHistoricalUnbound || e.EpochID != "") {
			return fmt.Errorf("%w: evidence epoch", ErrIntegrity)
		}
		if e.Request.Identity != m.Identity || e.Request.SourceID != identity.SourceID || e.Request.Role != identity.Role || e.Request.Optional != identity.Optional || publicationSourcePath(e.Request) != identity.Path {
			return fmt.Errorf("%w: evidence identity", ErrIntegrity)
		}
		if public.Withheld != nil || e.Original != nil {
			if _, err := rehydrateSource(public, e); err != nil {
				return fmt.Errorf("%w: evidence commitment", ErrIntegrity)
			}
		}
	}
	return nil
}
