package sessioncapture

import (
	"errors"
	"testing"
)

func TestDeliveryPostAckFaultNeverDowngradesPublished(t *testing.T) {
	for _, due := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "due"}[due], func(t *testing.T) {
			dir, o, r := setup(t)
			sentinel := errors.New("lost ack")
			o.Fault = func(stage string) error {
				if stage == "ack-committed" {
					return sentinel
				}
				return nil
			}
			q := open(t, dir, o)
			m := capture(t, q, r)
			backend := newBackend(t)
			var err error
			if due {
				err = q.DeliverDue(t.Context(), backend, r.Identity)
			} else {
				err = q.Deliver(t.Context(), backend, r.Identity)
			}
			if !errors.Is(err, sentinel) {
				t.Fatalf("post-ACK fault: %v", err)
			}
			var state string
			if e := q.db.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE session=? AND sequence=?`, r.Identity.BaseKey(), m.Sequence).Scan(&state); e != nil || state != "published" {
				t.Fatalf("post-ACK state: %s %v", state, e)
			}
		})
	}
}
