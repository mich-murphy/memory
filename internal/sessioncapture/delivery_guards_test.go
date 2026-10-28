package sessioncapture

import (
	"errors"
	"testing"
)

func TestEvidencePairRejectsSwappedEpochs(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	capture(t, q, r)
	write(t, r.Path, "rewrite\n")
	m := capture(t, q, r)
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].EpochID == es[1].EpochID {
		t.Fatalf("distinct epochs required: %v", es)
	}
	// Same Request coordinates do not bind different retained policy identities.
	es[0].Identities = []string{"historical-restriction"}
	es[1].Identities = []string{"active-alias"}
	es[0], es[1] = es[1], es[0]
	if err := validateEvidencePair(m, es, false); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("swapped epochs: %v", err)
	}
	if _, _, _, err := encodeAcceptedView(m, es); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("swapped encode: %v", err)
	}
}

func TestDeliveryFailureCannotResurrectTerminalJob(t *testing.T) {
	for _, state := range []string{"published", "superseded"} {
		for _, cause := range []error{ErrIntegrity, ErrDestination} {
			t.Run(state+cause.Error(), func(t *testing.T) {
				dir, o, r := setup(t)
				q := open(t, dir, o)
				if err := q.InitRuntime(t.Context()); err != nil {
					t.Fatal(err)
				}
				m := capture(t, q, r)
				if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state=? WHERE session=? AND sequence=?`, state, r.Identity.BaseKey(), m.Sequence); err != nil {
					t.Fatal(err)
				}
				for _, immediate := range []bool{false, true} {
					err := q.persistDeliveryFailure(t.Context(), r.Identity, m.Sequence, 1, cause, runtimeDefaults(RuntimeOptions{MaxAttempts: 1}), immediate)
					if errors.Is(cause, ErrIntegrity) && err != nil {
						t.Fatal(err)
					}
					var got string
					if e := q.db.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE session=? AND sequence=?`, r.Identity.BaseKey(), m.Sequence).Scan(&got); e != nil || got != state {
						t.Fatalf("terminal state: %s %v", got, e)
					}
					var n int
					if e := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM runtime_quarantine WHERE session=?`, r.Identity.BaseKey()).Scan(&n); e != nil || n != 0 {
						t.Fatalf("stale quarantine: %d %v", n, e)
					}
				}
			})
		}
	}
}

func TestImmediateTransientBeyondDaemonThresholdRemainsReplayable(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, r)
	opts := runtimeDefaults(RuntimeOptions{MaxAttempts: 1})
	err := q.deliveryResult(t.Context(), r.Identity, m.Sequence, 0, errors.New("network"), opts, true)
	if err == nil {
		t.Fatal("explicit attempt must return failure")
	}
	var state string
	if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE session=? AND sequence=?`, r.Identity.BaseKey(), m.Sequence).Scan(&state); err != nil || state != "retry" {
		t.Fatalf("threshold state: %s %v", state, err)
	}
}
