package sessioncapture

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// A bounded discovery cursor has already passed these requests when its callback
// returns. An expired callback budget must not silently discard their admission.
func TestRuntimeDiscoveryExpiredCallbackAdmitsReturnedRequestsWithoutFalseCompletion(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	options := runtimeDefaults(RuntimeOptions{
		OperationTimeout: 200 * time.Millisecond,
		DiscoverBatch: func(ctx context.Context) (DiscoveryBatch, error) {
			<-ctx.Done()
			return DiscoveryBatch{Requests: []Request{req}, Complete: true}, nil
		},
	})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 1 || sources[0].ObservedVersion != 1 {
		t.Fatalf("returned valid request was not admitted: %+v %v", sources, err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "timeout" || status.DiscoveryComplete || status.CompletedSweep != 0 {
		t.Fatalf("timed-out callback falsely completed discovery: %+v %v", status, err)
	}
}

func TestRuntimeDiscoveryExpiredCallbackPublishesAutonomously(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	backend := newBackend(t)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0 // DiscoverBatch is called serially by the discovery worker.
	done := make(chan error, 1)
	go func() {
		done <- q.Run(ctx, backend, RuntimeOptions{
			PollInterval: 10 * time.Millisecond, OperationTimeout: time.Second,
			DiscoverBatch: func(op context.Context) (DiscoveryBatch, error) {
				calls++
				if calls == 1 {
					<-op.Done()
					return DiscoveryBatch{Requests: []Request{req}}, nil
				}
				return DiscoveryBatch{}, nil // No later hint or rediscovery of the source.
			},
		})
	}()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		status, err := q.RuntimeStatus(t.Context())
		if err == nil && status.DiscoveryError == "timeout" && !status.DiscoveryComplete {
			jobs, jobErr := q.Status(t.Context())
			if jobErr == nil && len(jobs) == 1 && jobs[0].State == "published" {
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("runtime stopped before publication: %v", err)
		case <-deadline.C:
			status, _ := q.RuntimeStatus(t.Context())
			jobs, _ := q.Status(t.Context())
			t.Fatalf("timed-out returned request not published: runtime=%+v jobs=%+v", status, jobs)
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime workers did not stop")
	}
}

// Admission exhausts the save budget, then a later clean Complete batch must
// not resolve an issue observed in the preceding generation.
func TestRuntimeDiscoveryExpiredSaveCannotResolvePriorIssue(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, _ := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Complete: true}, nil)
	req.ObservedAtNS = 0 // Hint calls Now while preparing this request.
	var expire atomic.Bool
	o.Now = func() time.Time {
		if expire.CompareAndSwap(true, false) {
			// Expire admission before Hint can write the source row.
			time.Sleep(100 * time.Millisecond)
		}
		return time.Now()
	}
	q.opts.Now = o.Now
	options := runtimeDefaults(RuntimeOptions{OperationTimeout: 20 * time.Millisecond, DiscoverBatch: func(context.Context) (DiscoveryBatch, error) {
		expire.Store(true)
		return DiscoveryBatch{Requests: []Request{req}}, nil
	}})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatalf("release-budget failure recording did not succeed: %v", err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 0 {
		t.Fatalf("expired admission unexpectedly accepted source: %+v %v", sources, err)
	}
	options.DiscoverBatch = func(context.Context) (DiscoveryBatch, error) { return DiscoveryBatch{Complete: true}, nil }
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryComplete || status.DiscoveryError != "timeout" || status.ActiveDiscoveryIssues != 1 || status.CompletedSweep != 1 {
		t.Fatalf("failed admission/metadata became healthy coverage: %+v %v", status, err)
	}
}

func TestRuntimeDiscoveryUnrecordableFailureStopsLoop(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, _ := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Complete: true}, nil)
	oldRelease := runtimeReleaseTimeout
	runtimeReleaseTimeout = 20 * time.Millisecond
	defer func() { runtimeReleaseTimeout = oldRelease }()
	var expire atomic.Int32
	o.Now = func() time.Time {
		if expire.Add(-1) >= 0 {
			// Expire both the ordinary metadata write and its bounded
			// release-budget retry before either can commit.
			time.Sleep(100 * time.Millisecond)
		}
		return time.Now()
	}
	q.opts.Now = o.Now
	options := runtimeDefaults(RuntimeOptions{OperationTimeout: 20 * time.Millisecond, DiscoverBatch: func(context.Context) (DiscoveryBatch, error) {
		expire.Store(2)
		return DiscoveryBatch{Complete: true}, nil
	}})
	err := q.discoverLoop(t.Context(), options)
	if !errors.Is(err, errDiscoveryPersistence) || transientStepError(t.Context(), true, err) {
		t.Fatalf("unrecorded failure was swallowed: %v", err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryComplete || status.ActiveDiscoveryIssues != 1 || status.CompletedSweep != 1 {
		t.Fatalf("unrecorded failure resolved issue: %+v %v", status, err)
	}
}

func TestRuntimeDiscoveryParentCancellationDoesNotAdmitReturnedRequests(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	options := runtimeDefaults(RuntimeOptions{
		OperationTimeout: time.Second,
		DiscoverBatch: func(context.Context) (DiscoveryBatch, error) {
			cancel()
			return DiscoveryBatch{Requests: []Request{req}, Complete: true}, nil
		},
	})
	if err := q.discoverOnce(ctx, options); err != nil {
		t.Fatal(err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 0 {
		t.Fatalf("cancelled parent admitted requests: %+v %v", sources, err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryComplete || status.CompletedSweep != 0 {
		t.Fatalf("cancelled parent completed discovery: %+v %v", status, err)
	}
}
