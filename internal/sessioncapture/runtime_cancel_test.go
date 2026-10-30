package sessioncapture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// blockingBackend holds Current until its context ends, then reports that end.
type blockingBackend struct {
	publication.Backend
	started chan struct{}
	result  func(error) error
}

func (b blockingBackend) Current(ctx context.Context, _ string) ([]byte, string, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	if b.result != nil {
		return nil, "", b.result(ctx.Err())
	}
	return nil, "", ctx.Err()
}

func jobRow(t *testing.T, q *Queue) (string, int, int64) {
	t.Helper()
	var state string
	var attempts int
	var next int64
	if err := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,next_retry FROM jobs ORDER BY sequence LIMIT 1`).Scan(&state, &attempts, &next); err != nil {
		t.Fatal(err)
	}
	return state, attempts, next
}

func quarantineCount(t *testing.T, q *Queue) int {
	t.Helper()
	var n int
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_quarantine`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestShutdownCancellationKeepsDeliveryAttempts(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	capture(t, q, req)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET attempts=7`); err != nil {
		t.Fatal(err)
	}
	run, shutdown := context.WithCancel(t.Context())
	started := make(chan struct{}, 1)
	go func() { <-started; shutdown() }()
	op, cancel := context.WithTimeout(run, time.Minute)
	defer cancel()
	b := blockingBackend{Backend: newBackend(t), started: started}
	_, err := q.deliverDueWork(op, b, req.Identity, runtimeDefaults(RuntimeOptions{}))
	if runtimeStopError(run, err) != nil {
		t.Fatalf("shutdown must be a clean stop: %v", err)
	}
	state, attempts, next := jobRow(t, q)
	if attempts != 7 || state == "quarantined" || next != 0 || quarantineCount(t, q) != 0 {
		t.Fatalf("state=%s attempts=%d next=%d", state, attempts, next)
	}
	// After a restart the job is still due and delivers.
	if err = q.DeliverDue(t.Context(), newBackend(t), req.Identity); err != nil {
		t.Fatal(err)
	}
	if state, _, _ = jobRow(t, q); state != "published" {
		t.Fatalf("state %s", state)
	}
}

func TestOperationDeadlineStillSpendsAttempt(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	capture(t, q, req)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET attempts=0`); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{Jitter: func(d time.Duration) time.Duration { return d }})
	b := blockingBackend{Backend: newBackend(t)}
	op, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := q.deliverDueWork(op, b, req.Identity, opts); err != nil {
		t.Fatal(err)
	}
	state, attempts, _ := jobRow(t, q)
	var reason string
	if err := q.db.QueryRowContext(t.Context(), `SELECT last_error FROM jobs LIMIT 1`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if state != "retry" || attempts != 1 || reason != string(FailureTimeout) {
		t.Fatalf("%s %d %s", state, attempts, reason)
	}
	// The 8th deadline failure quarantines.
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET attempts=7,next_retry=0,state='retry'`); err != nil {
		t.Fatal(err)
	}
	op2, cancel2 := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel2()
	if _, err := q.deliverDueWork(op2, b, req.Identity, opts); err != nil {
		t.Fatal(err)
	}
	entries, err := q.Quarantine(t.Context(), 0, 8)
	state, attempts, _ = jobRow(t, q)
	if err != nil || len(entries) != 1 || entries[0].Reason != string(FailureTimeout) || state != "quarantined" || attempts != 8 {
		t.Fatalf("%+v %v %s %d", entries, err, state, attempts)
	}
}

func TestFatalCauseDuringShutdownStillStopsRun(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	capture(t, q, req)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	started := make(chan struct{}, 1)
	go func() { <-started; shutdown() }()
	b := blockingBackend{Backend: newBackend(t), started: started, result: func(e error) error { return errors.Join(e, publication.ErrDurability) }}
	opts := runtimeDefaults(RuntimeOptions{})
	err := q.runtimeWorker(run, opts, func(op context.Context) (bool, error) {
		return q.deliverDueWork(op, b, req.Identity, opts)
	})
	if !errors.Is(err, publication.ErrDurability) {
		t.Fatalf("durability failure swallowed: %v", err)
	}
}

func TestShutdownCancellationKeepsCaptureClaimRetryable(t *testing.T) {
	dir, o, req := setup(t)
	run, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	fail := true
	o.Fault = func(point string) error {
		if fail && point == "payload-before-sync" {
			shutdown()
			return context.Canceled
		}
		return nil
	}
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{})
	op, cancel := context.WithTimeout(run, time.Minute)
	defer cancel()
	_, err := q.captureNext(op, opts)
	if runtimeStopError(run, err) != nil {
		t.Fatalf("shutdown must be a clean stop: %v", err)
	}
	var attempts, claim int64
	var state string
	if e := q.db.QueryRowContext(t.Context(), `SELECT attempts,claim,state FROM runtime_sources LIMIT 1`).Scan(&attempts, &claim, &state); e != nil {
		t.Fatal(e)
	}
	if attempts != 0 || claim != 0 || state == "quarantined" || quarantineCount(t, q) != 0 {
		t.Fatalf("attempts=%d claim=%d state=%s", attempts, claim, state)
	}
	fail = false
	// The scan cursor wraps on the first empty pass, as after any idle sweep.
	q.runtimeLocal().sourceCursor = 0
	if ok, e := q.captureNext(t.Context(), opts); e != nil || !ok {
		t.Fatalf("restart capture %v %v", ok, e)
	}
	if s := rowOf(t, q, req); s.state != "captured" {
		t.Fatalf("%+v", s)
	}
}

func TestReplayDoesNotReviveResolvedDelivery(t *testing.T) {
	for _, resolved := range []string{"superseded", "published"} {
		t.Run(resolved, func(t *testing.T) {
			dir, o, req := setup(t)
			q := open(t, dir, o)
			capture(t, q, req)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := q.runtimeDeliveryFailure(t.Context(), req.Identity, 1, 1, ErrIntegrity, runtimeDefaults(RuntimeOptions{MaxAttempts: 1})); err != nil {
				t.Fatal(err)
			}
			if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state=?`, resolved); err != nil {
				t.Fatal(err)
			}
			err := q.Replay(t.Context(), 1)
			if !errors.Is(err, ErrReplayResolved) {
				t.Fatalf("%v", err)
			}
			if state, attempts, _ := jobRow(t, q); state != resolved || attempts != 0 {
				t.Fatalf("revived: %s %d", state, attempts)
			}
			entries, err := q.Quarantine(t.Context(), 0, 8)
			if err != nil || len(entries) != 1 || entries[0].Active {
				t.Fatalf("%+v %v", entries, err)
			}
			if err = q.Replay(t.Context(), 1); err == nil || errors.Is(err, ErrReplayResolved) {
				t.Fatalf("second replay: %v", err)
			}
		})
	}
}

func TestReplayCaptureGuard(t *testing.T) {
	for _, current := range []string{"quarantined", "captured", "pending"} {
		t.Run(current, func(t *testing.T) {
			dir, o, req := setup(t)
			o.Fault = func(point string) error {
				if point == "payload-before-sync" {
					return errors.New("injected")
				}
				return nil
			}
			q := open(t, dir, o)
			if err := q.Hint(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			if _, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{MaxAttempts: 1})); err != nil {
				t.Fatal(err)
			}
			if s := rowOf(t, q, req); s.state != "quarantined" {
				t.Fatalf("%+v", s)
			}
			if _, err := q.db.ExecContext(t.Context(), `UPDATE runtime_sources SET state=?`, current); err != nil {
				t.Fatal(err)
			}
			err := q.Replay(t.Context(), 1)
			want := "pending"
			if current != "quarantined" {
				want = current
				if !errors.Is(err, ErrReplayResolved) {
					t.Fatalf("%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if s := rowOf(t, q, req); s.state != want {
				t.Fatalf("state %s want %s", s.state, want)
			}
			entries, qerr := q.Quarantine(t.Context(), 0, 8)
			if qerr != nil || len(entries) != 1 || entries[0].Active {
				t.Fatalf("%+v %v", entries, qerr)
			}
		})
	}
}
