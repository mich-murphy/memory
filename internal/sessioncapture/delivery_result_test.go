package sessioncapture

import (
	"context"
	"errors"
	"testing"
)

func TestDeliveryResultCancellationAndFailureBothAPIs(t *testing.T) {
	for _, due := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "due"}[due], func(t *testing.T) {
			dir, o, r := setup(t)
			q := open(t, dir, o)
			m := capture(t, q, r)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := q.deliveryResult(ctx, r.Identity, m.Sequence, 0, context.Canceled, runtimeDefaults(RuntimeOptions{}), !due); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			st, err := q.Status(t.Context())
			if err != nil || st[0].Attempts != 0 || st[0].State != "retry" {
				t.Fatalf("shutdown: %v %v", st, err)
			}
			if err := q.deliveryResult(t.Context(), r.Identity, m.Sequence, 0, ErrIntegrity, runtimeDefaults(RuntimeOptions{}), !due); !due && !errors.Is(err, ErrIntegrity) {
				t.Fatalf("failure: %v", err)
			}
			st, err = q.Status(t.Context())
			if err != nil || st[0].LastError != string(FailureIntegrity) {
				t.Fatalf("classification: %v %v", st, err)
			}
			if due && st[0].State != "retry" || !due && st[0].State != "integrity-error" {
				t.Fatalf("replay state: %v", st)
			}
		})
	}
}

func TestMalformedEnrichmentSpecQuarantinedOnce(t *testing.T) {
	dir, o, _ := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	if _, err := q.db.ExecContext(t.Context(), `INSERT INTO enrichment_work(id,session,spec,contract,state) VALUES('bad','s',x'ff','c','pending')`); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{})
	for range 2 {
		_, err := q.enrichmentNext(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
	}
	var state, reason string
	var attempts int
	if err := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,last_error FROM enrichment_work WHERE id='bad'`).Scan(&state, &attempts, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "quarantined" || attempts != 1 || reason != "integrity-error" {
		t.Fatalf("%s %d %s", state, attempts, reason)
	}
}
