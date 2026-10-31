package sessioncapture

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// Both boundaries use channels, not timing: raw N is selected, another caller
// really publishes it, and Capture makes selected optional N+1 the pending head.
// The old raw selection must not spend an attempt or perform optional network I/O.
func TestRuntimeDeliveryLaneTransitions(t *testing.T) {
	for _, window := range []struct {
		name, boundary string
		worker         bool
	}{
		{"worker-selection-to-helper", "delivery-worker-lane-selected", true},
		{"helper-selection-to-locked-head", "delivery-lane-selected", false},
	} {
		t.Run(window.name, func(t *testing.T) {
			dir, options, raw := setup(t)
			selected, resume := make(chan struct{}), make(chan struct{})
			var selectedOnce atomic.Bool
			var observe atomic.Bool
			var networkBoundaries atomic.Int64
			options.Fault = func(boundary string) error {
				if observe.Load() && (boundary == "upload-before" || boundary == "current-before") {
					networkBoundaries.Add(1)
				}
				if boundary == window.boundary && selectedOnce.CompareAndSwap(false, true) {
					close(selected)
					select {
					case <-resume:
					case <-t.Context().Done():
						return t.Context().Err()
					}
				}
				return nil
			}
			q := open(t, dir, options)
			first := capture(t, q, raw)
			if hasSelectedOptional(first) {
				t.Fatal("initial head is not raw")
			}
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			backend := newBackend(t)
			runtimeOptions := runtimeDefaults(RuntimeOptions{})
			type result struct {
				did bool
				err error
			}
			results := make(chan result, 1)
			done := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(resume) }) }
			go func() {
				defer close(done)
				var did bool
				var err error
				if window.worker {
					did, err = q.deliveryLaneNext(t.Context(), backend, runtimeOptions, false)
				} else {
					did, err = q.deliverDueWork(t.Context(), backend, raw.Identity, runtimeOptions)
				}
				results <- result{did, err}
			}()
			// Registered after open: always release and join before closing the DB,
			// including a failing setup assertion while the caller is at the barrier.
			t.Cleanup(func() {
				release()
				<-done
			})
			select {
			case <-selected:
			case outcome := <-results:
				t.Fatalf("caller missed selection barrier: %+v", outcome)
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}

			// No SQL mutation simulates a successful attempt: the actual immediate
			// delivery publishes N through the local durable backend and ACK path.
			if err := q.Deliver(t.Context(), backend, raw.Identity); err != nil {
				t.Fatal(err)
			}
			assertDeliveryLaneExport(t, backend, first, map[string]string{raw.SourceID: "first\n"})
			optional := raw
			optional.SourceID, optional.Role, optional.Optional = "optional", "artifact", true
			optional.Path = filepath.Join(t.TempDir(), "artifact")
			const artifact = "optional evidence\n"
			write(t, optional.Path, artifact)
			next := capture(t, q, optional)
			if !hasSelectedOptional(next) || next.Sequence != first.Sequence+1 {
				t.Fatal("capture did not create the selected optional successor")
			}
			if changed, err := q.RefreshOptionalSelection(t.Context(), raw.Identity); err != nil || changed {
				t.Fatalf("healthy selected optional view changed: changed=%v err=%v", changed, err)
			}
			before, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != 2 || before[0].State != "published" || before[0].Attempts != 1 || before[1].State != "pending" || before[1].Attempts != 0 {
				t.Fatalf("unexpected transition setup: %+v", before)
			}
			observe.Store(true)
			release()
			var outcome result
			select {
			case outcome = <-results:
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}
			observe.Store(false)
			if outcome.err != nil || outcome.did {
				t.Errorf("changed head must leave the old lane idle: did=%v err=%v", outcome.did, outcome.err)
			}
			after, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || networkBoundaries.Load() != 0 {
				t.Errorf("wrong-lane attempt: before=%+v after=%+v network_boundaries=%d", before, after, networkBoundaries.Load())
			}
			assertDeliveryLaneExport(t, backend, first, map[string]string{raw.SourceID: "first\n"})

			if window.worker {
				// The optional worker, not a raw worker that reselects its lane,
				// must make the actual attempt through the same runtime path.
				did, err := q.deliveryLaneNext(t.Context(), backend, runtimeOptions, true)
				if err != nil || !did {
					t.Fatalf("owning optional worker: did=%v err=%v", did, err)
				}
			} else if err := q.DeliverDue(t.Context(), backend, raw.Identity); err != nil {
				// Explicit DeliverDue deliberately chooses the current optional lane.
				t.Fatal(err)
			}
			assertDeliveryLaneExport(t, backend, next, map[string]string{raw.SourceID: "first\n", optional.SourceID: artifact})
			final, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(final) != 2 || final[0].State != "published" || final[1].State != "published" || final[0].Attempts != 1 || final[1].Attempts != 1 {
				t.Fatalf("each actual publication must spend exactly one attempt: %+v", final)
			}
		})
	}
}

func assertDeliveryLaneExport(t *testing.T, backend publication.Backend, want publication.Manifest, bodies map[string]string) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "export")
	exported, err := publication.ExportSession(t.Context(), backend, want.Identity, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.Manifest, want) {
		t.Errorf("restored publication differs: got=%s/%d want=%s/%d", exported.Manifest.PublicationID, exported.Manifest.Sequence, want.PublicationID, want.Sequence)
	}
	if len(exported.Bodies) != len(bodies) {
		t.Errorf("restored body count=%d want=%d", len(exported.Bodies), len(bodies))
	}
	for _, body := range exported.Bodies {
		got, err := os.ReadFile(filepath.Join(dest, body.Path))
		if err != nil {
			t.Fatal(err)
		}
		wantBody, ok := bodies[body.SourceID]
		if !ok || string(got) != wantBody {
			t.Errorf("restored source %q bytes=%q want=%q", body.SourceID, got, wantBody)
		}
	}
}

func TestRuntimeDeliveryLaneFaults(t *testing.T) {
	for _, boundary := range []string{"delivery-worker-lane-selected", "delivery-lane-selected"} {
		t.Run(boundary, func(t *testing.T) {
			dir, options, raw := setup(t)
			sentinel := errors.New("synthetic lane boundary failure")
			options.Fault = func(stage string) error {
				if stage == boundary {
					return sentinel
				}
				return nil
			}
			q := open(t, dir, options)
			capture(t, q, raw)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			backend := newBackend(t)
			before, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var did bool
			if boundary == "delivery-worker-lane-selected" {
				did, err = q.deliveryLaneNext(t.Context(), backend, runtimeDefaults(RuntimeOptions{}), false)
			} else {
				did, err = q.deliverDueWork(t.Context(), backend, raw.Identity, runtimeDefaults(RuntimeOptions{}))
			}
			if did || !errors.Is(err, sentinel) {
				t.Fatalf("fault must stop before attempt: did=%v err=%v", did, err)
			}
			after, err := q.Status(t.Context())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("fault spent attempt/state: before=%+v after=%+v err=%v", before, after, err)
			}
			if _, _, err := backend.Current(t.Context(), raw.Identity.CurrentKey()); !errors.Is(err, publication.ErrNotFound) {
				t.Fatalf("fault published a current root: %v", err)
			}
		})
	}
}

func TestDeliverDuePausedMalformedHead(t *testing.T) {
	dir, options, raw := setup(t)
	q := open(t, dir, options)
	capture(t, q, raw)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), "UPDATE jobs SET manifest=?", []byte("invalid metadata")); err != nil {
		t.Fatal(err)
	}
	if err := q.pauseDestination(t.Context()); err != nil {
		t.Fatal(err)
	}
	backend := newBackend(t)
	if err := q.DeliverDue(t.Context(), backend, raw.Identity); err != nil {
		t.Fatalf("paused destination must remain idle before decoding head: %v", err)
	}
	assertDeliveryLaneJobState(t, q, "pending", 0)
	if err := q.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Explicit delivery still reports malformed metadata; the coordinator's
	// existing dueIdentity path quarantines that coordinate without an attempt.
	if err := q.DeliverDue(t.Context(), backend, raw.Identity); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("unpaused explicit delivery lost integrity error: %v", err)
	}
	if did, err := q.deliveryLaneNext(t.Context(), backend, runtimeDefaults(RuntimeOptions{}), false); err != nil || did {
		t.Fatalf("malformed scheduler head: did=%v err=%v", did, err)
	}
	assertDeliveryLaneJobState(t, q, "quarantined", 0)
}

func TestRuntimeDeliveryLaneMalformedReread(t *testing.T) {
	dir, options, raw := setup(t)
	var q *Queue
	options.Fault = func(stage string) error {
		if stage == "delivery-lane-selected" {
			_, err := q.db.ExecContext(t.Context(), "UPDATE jobs SET manifest=?", []byte("invalid metadata"))
			return err
		}
		return nil
	}
	q = open(t, dir, options)
	capture(t, q, raw)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	optionsRuntime := runtimeDefaults(RuntimeOptions{MaxAttempts: 1})
	if did, err := q.deliverDueWork(t.Context(), newBackend(t), raw.Identity, optionsRuntime); err != nil || !did {
		t.Fatalf("malformed locked head must retain attempt/quarantine path: did=%v err=%v", did, err)
	}
	assertDeliveryLaneJobState(t, q, "quarantined", 1)
}

func assertDeliveryLaneJobState(t *testing.T, q *Queue, wantState string, wantAttempts int) {
	t.Helper()
	var state string
	var attempts int
	if err := q.db.QueryRowContext(t.Context(), "SELECT state,attempts FROM jobs").Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != wantState || attempts != wantAttempts {
		t.Fatalf("job state=%s attempts=%d want=%s/%d", state, attempts, wantState, wantAttempts)
	}
}
