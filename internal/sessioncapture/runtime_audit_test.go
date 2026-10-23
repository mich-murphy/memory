package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeAuditStartupCancellation(t *testing.T) {
	for _, boundary := range []string{"pre-canceled", "runtime-init-statement", "runtime-sweep-before"} {
		for _, joined := range []bool{false, true} {
			if boundary == "pre-canceled" && joined {
				continue
			}
			t.Run(fmt.Sprintf("%s/joined=%v", boundary, joined), func(t *testing.T) {
				dir, o, _ := setup(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := errors.New("durability failure")
				o.Fault = func(point string) error {
					if point != boundary {
						return nil
					}
					cancel()
					if joined {
						return errors.Join(context.Canceled, failure)
					}
					return nil
				}
				q := open(t, dir, o)
				if boundary == "pre-canceled" {
					cancel()
				}
				err := q.Run(ctx, newBackend(t), RuntimeOptions{})
				if joined {
					if !errors.Is(err, failure) {
						t.Fatalf("lost failure: %v", err)
					}
				} else if err != nil {
					t.Fatalf("pure shutdown: %v", err)
				}
				// Partial schema initialization must remain retryable on this Queue.
				q.opts.Fault = nil
				if err := q.InitRuntime(t.Context()); err != nil {
					t.Fatal(err)
				}
				if q.runtimeLocal().running {
					t.Fatal("running flag retained")
				}
			})
		}
	}
}

func TestRuntimeAuditCancellationErrorTree(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	failure := errors.New("fsync failure")
	wrapped := fmt.Errorf("database operation: %w", context.Canceled)
	if runtimeStopError(ctx, wrapped) == nil {
		t.Fatal("cancellation without caller cancellation suppressed")
	}
	cancel()
	for _, err := range []error{context.Canceled, wrapped, errors.Join(wrapped, context.Canceled)} {
		if runtimeStopError(ctx, err) != nil {
			t.Fatalf("pure cancellation retained: %v", err)
		}
	}
	for _, err := range []error{failure, errors.Join(wrapped, failure), errors.Join(context.Canceled, ErrIntegrity), context.DeadlineExceeded} {
		if runtimeStopError(ctx, err) == nil {
			t.Fatalf("non-cancellation suppressed: %v", err)
		}
	}
}

func TestRuntimeAuditRunJoinsWorkers(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var active atomic.Int32
	done := make(chan error, 1)
	backend := newBackend(t)
	go func() {
		done <- q.Run(ctx, backend, RuntimeOptions{DiscoverBatch: func(ctx context.Context) (DiscoveryBatch, error) {
			active.Add(1)
			defer active.Add(-1)
			close(started)
			<-ctx.Done()
			<-release
			return DiscoveryBatch{}, ctx.Err()
		}})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("Run returned before worker stopped: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if active.Load() != 0 {
		t.Fatal("worker leaked")
	}
	// No stale running flag or claims prevent another normal lifecycle.
	stopped, stop := context.WithCancel(t.Context())
	stop()
	if err := q.Run(stopped, newBackend(t), RuntimeOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAuditDiscoveryRequestBackpressure(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, _ := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Complete: true}, nil)
	requests := make([]Request, MaxDiscoveryRecords+1)
	for j := range requests {
		requests[j] = req
	}
	options := runtimeDefaults(RuntimeOptions{DiscoverBatch: func(context.Context) (DiscoveryBatch, error) {
		return DiscoveryBatch{Requests: requests, Complete: true}, nil
	}})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.DiscoveryComplete || status.ActiveDiscoveryIssues != 1 || status.DiscoveryError != "quota" {
		t.Fatalf("false complete sweep: %+v", status)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 1)
	if err != nil || len(sources) != 1 || sources[0].ObservedVersion != MaxDiscoveryRecords {
		t.Fatalf("unbounded request admission: %+v %v", sources, err)
	}
}

func TestRuntimeAuditQuarantineSoftwareVersion(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	capture(t, q, req)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.pauseDestination(t.Context()); err != nil {
		t.Fatal(err)
	}
	for index, version := range []string{"audit-v1", "audit-v2"} {
		ctx, cancel := context.WithCancel(t.Context())
		err := q.Run(ctx, newBackend(t), RuntimeOptions{SoftwareVersion: version, DiscoverBatch: func(op context.Context) (DiscoveryBatch, error) {
			defer cancel()
			err := q.runtimeDeliveryFailure(op, req.Identity, 1, 1, ErrIntegrity, runtimeDefaults(RuntimeOptions{MaxAttempts: 1}))
			return DiscoveryBatch{}, err
		}})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		view, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 64)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Quarantine) != index+1 || view.Quarantine[0].SoftwareVersion != "audit-v1" || view.Quarantine[index].SoftwareVersion != version {
			t.Fatalf("version history: %+v", view.Quarantine)
		}
		if err := q.Replay(t.Context(), view.Quarantine[index].ID); err != nil {
			t.Fatal(err)
		}
		entries, err := q.Quarantine(t.Context(), 0, 64)
		if err != nil || entries[index].Active || entries[index].SoftwareVersion != version {
			t.Fatalf("replay changed provenance: %+v %v", entries, err)
		}
	}
}

func TestRuntimeAuditAdditiveSoftwareVersion(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	capture(t, q, req)
	// Reproduce the existing experimental table, without discarding any queue data.
	_, err := q.db.ExecContext(t.Context(), `CREATE TABLE runtime_quarantine (id INTEGER PRIMARY KEY,kind TEXT NOT NULL,source_id INTEGER NOT NULL DEFAULT 0,session TEXT NOT NULL,sequence INTEGER NOT NULL DEFAULT 0,request BLOB,reason TEXT NOT NULL,attempts INTEGER NOT NULL,created_at INTEGER NOT NULL,contract TEXT NOT NULL,active INTEGER NOT NULL DEFAULT 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.db.ExecContext(t.Context(), `INSERT INTO runtime_quarantine(kind,session,sequence,reason,attempts,created_at,contract) VALUES('delivery',?,1,'integrity-error',1,1,'session-publication/v1')`, req.Identity.BaseKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := q.Quarantine(t.Context(), 0, 64)
	if err != nil || len(entries) != 1 || entries[0].SoftwareVersion != "unknown/devel" || !entries[0].Active {
		t.Fatalf("upgrade lost history: %+v %v", entries, err)
	}
	jobs, err := q.Status(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].State == "published" {
		t.Fatalf("upgrade changed accepted job: %+v %v", jobs, err)
	}
}

func TestRuntimeAuditJoinedWorkerShutdownFailure(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failure := errors.New("metadata integrity failure")
	err := q.runtimeWorker(ctx, runtimeDefaults(RuntimeOptions{}), func(context.Context) (bool, error) {
		cancel()
		return false, errors.Join(context.Canceled, failure)
	})
	if !errors.Is(err, failure) {
		t.Fatalf("lost joined worker shutdown failure: %v", err)
	}
}
