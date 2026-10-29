package sessioncapture

import (
	"context"
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
