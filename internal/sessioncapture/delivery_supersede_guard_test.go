package sessioncapture

import (
	"errors"
	"testing"
)

func TestDeliveryFailureAfterSupersedeCannotReviveJob(t *testing.T) {
	for _, due := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "due"}[due], func(t *testing.T) {
			dir, o, r := setup(t)
			failure := errors.New("remote interrupted after supersede")
			var q *Queue
			o.Fault = func(stage string) error {
				if stage != "upload-before" {
					return nil
				}
				if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state='superseded' WHERE session=? AND sequence=1`, r.Identity.BaseKey()); err != nil {
					return err
				}
				return failure
			}
			q = open(t, dir, o)
			m := capture(t, q, r)
			backend := newBackend(t)
			var err error
			if due {
				err = q.DeliverDue(t.Context(), backend, r.Identity)
			} else {
				err = q.Deliver(t.Context(), backend, r.Identity)
			}
			if !due && !errors.Is(err, failure) {
				t.Fatalf("explicit failure: %v", err)
			}
			if due && err != nil {
				t.Fatalf("runtime retry scheduling: %v", err)
			}
			var state string
			if e := q.db.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE session=? AND sequence=?`, r.Identity.BaseKey(), m.Sequence).Scan(&state); e != nil || state != "superseded" {
				t.Fatalf("stale state: %s %v", state, e)
			}
		})
	}
}
