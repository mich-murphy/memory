package sessioncapture

import (
	"context"
	"errors"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestOptionalIsolationRejectsFatalErrorTrees(t *testing.T) {
	for _, stage := range []string{"upload", "verify", "object-fence"} {
		for _, cause := range []error{publication.ErrDurability, context.Canceled, context.DeadlineExceeded, errors.Join(errors.New("transport"), context.Canceled), errors.Join(context.DeadlineExceeded, publication.ErrDurability)} {
			q, r, m := optionalFixture(t)
			_, es, err := q.latest(t.Context(), r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			dep := &publication.SourceDependencyError{Optional: true, SourceKeyHash: publication.SourceKeyHash(m.Sources[1]), EpochID: m.Sources[1].EpochID, Stage: stage, Err: cause}
			handled, err := q.isolateOptionalFailure(t.Context(), r.Identity, m, es, dep)
			if handled || err != nil {
				t.Fatalf("downgraded fatal tree at %s: %v", stage, err)
			}
			var count int
			if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM optional_repairs`).Scan(&count); err != nil || count != 0 {
				t.Fatal("false repair failure latch", err)
			}
		}
	}
}
