package sessioncapture

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestOptionalIsolationAcceptsGenuineOperationExpiry(t *testing.T) {
	expired := func(t *testing.T) context.Context {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		t.Cleanup(cancel)
		return ctx
	}
	latches := func(t *testing.T, q *Queue) int {
		var n int
		if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM optional_repairs`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, stage := range []string{"upload", "verify", "object-fence"} {
		t.Run(stage, func(t *testing.T) {
			cases := []struct {
				name     string
				cause    error
				optional bool
				ctx      func(*testing.T) context.Context
				want     bool
			}{
				{"expired", context.DeadlineExceeded, true, expired, true},
				{"joined deadlines", errors.Join(context.DeadlineExceeded, context.DeadlineExceeded), true, expired, true},
				{"live", context.DeadlineExceeded, true, func(t *testing.T) context.Context { return t.Context() }, false},
				{"durability", errors.Join(context.DeadlineExceeded, publication.ErrDurability), true, expired, false},
				{"canceled", errors.Join(context.DeadlineExceeded, context.Canceled), true, expired, false},
				{"required", context.DeadlineExceeded, false, expired, false},
			}
			for _, c := range cases {
				q, r, m := optionalFixture(t)
				_, es, err := q.latest(t.Context(), r.Identity)
				if err != nil {
					t.Fatal(err)
				}
				dep := &publication.SourceDependencyError{Optional: c.optional, SourceKeyHash: publication.SourceKeyHash(m.Sources[1]), EpochID: m.Sources[1].EpochID, Stage: stage, Err: c.cause}
				handled, err := q.isolateOptionalFailure(c.ctx(t), r.Identity, m, es, dep)
				if c.want {
					if !handled || !errors.Is(err, errSelectionSuperseded) || latches(t, q) != 1 {
						t.Fatalf("%s: not isolated: handled=%v err=%v", c.name, handled, err)
					}
				} else if handled || err != nil || latches(t, q) != 0 {
					t.Fatalf("%s: wrongly isolated: handled=%v err=%v", c.name, handled, err)
				}
			}
		})
	}
}
