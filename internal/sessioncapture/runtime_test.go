package sessioncapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

type runtimeBrokenBackend struct {
	publication.Backend
	err error
}

func (b runtimeBrokenBackend) Current(context.Context, string) ([]byte, string, error) {
	return nil, "", b.err
}

func TestRuntimeRetryPauseQuarantineReplay(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	capture(t, q, req)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	broken := runtimeBrokenBackend{Backend: newBackend(t), err: errors.New("safe transient")}
	options := runtimeDefaults(RuntimeOptions{MaxAttempts: 2, Jitter: func(d time.Duration) time.Duration { return d }})
	if err := q.deliverDue(t.Context(), broken, req.Identity, options); err != nil {
		t.Fatal(err)
	}
	st, err := q.Status(t.Context())
	if err != nil || st[0].Attempts != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if err = q.deliverDue(t.Context(), broken, req.Identity, options); err != nil {
		t.Fatal(err)
	}
	st, err = q.Status(t.Context())
	if err != nil || st[0].Attempts != 1 {
		t.Fatalf("due bypass %+v %v", st, err)
	}
	if _, err = q.db.ExecContext(t.Context(), "UPDATE jobs SET next_retry=0"); err != nil {
		t.Fatal(err)
	}
	if err = q.deliverDue(t.Context(), broken, req.Identity, options); err != nil {
		t.Fatal(err)
	}
	quarantines, err := q.Quarantine(t.Context(), 0, 64)
	if err != nil || len(quarantines) != 1 {
		t.Fatalf("%+v %v", quarantines, err)
	}
	if err = q.Replay(t.Context(), quarantines[0].ID); err != nil {
		t.Fatal(err)
	}
	broken.err = fmt.Errorf("credential must never persist: %w", ErrDestination)
	if err = q.deliverDue(t.Context(), broken, req.Identity, options); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || !status.Paused || status.PausedReason != "destination" {
		t.Fatalf("%+v %v", status, err)
	}
	if err = q.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = q.DeliverDue(t.Context(), newBackend(t), req.Identity); err != nil {
		t.Fatal(err)
	}
	st, err = q.Status(t.Context())
	if err != nil || st[0].State != "published" {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestRuntimeRestartAndNewerHint(t *testing.T) {
	dir, o, req := setup(t)
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	w, err := q.claimSource(t.Context())
	if err != nil || w.id == 0 {
		t.Fatalf("%+v %v", w, err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	q = open(t, dir, o)
	if err = q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	w, err = q.claimSource(t.Context())
	if err != nil || w.id == 0 {
		t.Fatalf("restart %+v %v", w, err)
	}
	if err = q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err = q.db.ExecContext(t.Context(), "UPDATE runtime_sources SET claim=0"); err != nil {
		t.Fatal(err)
	}
	q.runtimeLocal().sourceCursor = 0
	if _, err = q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil {
		t.Fatal(err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || sources[0].CapturedVersion != 2 {
		t.Fatalf("%+v %v", sources, err)
	}
	if err = q.HintAbsent(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	status, err := q.Status(t.Context())
	if err != nil || len(status) != 1 {
		t.Fatalf("absence deleted bytes %+v %v", status, err)
	}
}

func TestRuntimeDiscoveryPartialAndInboxConcurrent(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := WriteHint(t.Context(), dir, req); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 1 {
		t.Fatalf("%+v %v", sources, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	backend := newBackend(t)
	go func() {
		done <- q.Run(ctx, backend, RuntimeOptions{PollInterval: 10 * time.Millisecond, OperationTimeout: time.Second, Discover: func(context.Context) ([]Request, error) {
			return []Request{req}, errors.New("private path must not persist")
		}})
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		status, e := q.RuntimeStatus(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		jobs, e := q.Status(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if len(jobs) > 0 && jobs[0].State == "published" && status.DiscoveryError == "transient" {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			t.Fatal("no autonomous publication")
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
		t.Fatal("workers leaked")
	}
}

func TestRuntimeFairPageAndSessionClaims(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	for n := range 70 {
		r := req
		r.SourceID = strconv.Itoa(n)
		if err := q.Hint(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	w, err := q.claimSource(t.Context())
	if err != nil || w.id == 0 {
		t.Fatalf("%+v %v", w, err)
	}
	if second, e := q.claimSource(t.Context()); e != nil || second.id != 0 {
		t.Fatalf("same session duplicate claim %+v %v", second, e)
	}
	b := req
	b.Identity.SessionID = "healthy"
	if err = q.Hint(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	w, err = q.claimSource(t.Context())
	if err != nil || w.req.Identity.SessionID != "healthy" {
		t.Fatalf("healthy session blocked %+v %v", w, err)
	}
}

func TestRuntimePolicyRetryAndAbsenceRetainEvidence(t *testing.T) {
	dir, o, req := setup(t)
	denied := true
	o.CheckPolicy = func(context.Context, Request) error {
		if denied {
			return ErrPolicy
		}
		return nil
	}
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	options := runtimeDefaults(RuntimeOptions{MaxAttempts: 3})
	if _, err := q.captureNext(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || sources[0].LastError != "policy-blocked" {
		t.Fatalf("%+v %v", sources, err)
	}
	denied = false
	if _, err = q.db.ExecContext(t.Context(), "UPDATE runtime_sources SET next_retry=0"); err != nil {
		t.Fatal(err)
	}
	// Rotation at the page boundary takes one idle scheduler turn, not a new hint.
	for range 2 {
		if _, err = q.captureNext(t.Context(), options); err != nil {
			t.Fatal(err)
		}
	}
	sources, err = q.WorkStatus(t.Context(), 0, 64)
	if err != nil || sources[0].State != "captured" {
		t.Fatalf("%+v %v", sources, err)
	}
	req.Path += ".missing"
	if err = q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = q.captureNext(t.Context(), options); err != nil {
			t.Fatal(err)
		}
	}
	sources, err = q.WorkStatus(t.Context(), 0, 64)
	if err != nil || sources[0].State != "absent" || sources[0].CapturedVersion == 0 {
		t.Fatalf("%+v %v", sources, err)
	}
	jobs, err := q.Status(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("evidence changed %+v %v", jobs, err)
	}
}

func TestRuntimeInboxRestartWithoutHook(t *testing.T) {
	dir, o, req := setup(t)
	// A hook can durably enqueue before there is an open coordinator at all.
	if err := WriteHint(t.Context(), dir, req); err != nil {
		t.Fatal(err)
	}
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	q = open(t, dir, o)
	if err = q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if did, e := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); e != nil || !did {
		t.Fatalf("restart did=%v err=%v", did, e)
	}
	jobs, err := q.Status(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("%+v %v", jobs, err)
	}
}

type runtimeTimeoutBackend struct {
	publication.Backend
	blocked string
}

func (b runtimeTimeoutBackend) Current(ctx context.Context, key string) ([]byte, string, error) {
	if key == b.blocked {
		<-ctx.Done()
		return nil, "", ctx.Err()
	}
	return b.Backend.Current(ctx, key)
}

func TestRuntimeTimeoutSessionAllowsHealthySession(t *testing.T) {
	dir, o, a := setup(t)
	q := open(t, dir, o)
	capture(t, q, a)
	b := a
	b.Identity.SessionID = "healthy"
	capture(t, q, b)
	backend := runtimeTimeoutBackend{Backend: newBackend(t), blocked: a.Identity.CurrentKey()}
	options := runtimeDefaults(RuntimeOptions{OperationTimeout: time.Second})
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, err := q.deliveryNext(ctx, backend, options)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = q.deliveryNext(ctx, backend, options); err != nil {
		t.Fatal(err)
	}
	jobs, err := q.Status(t.Context())
	if err != nil || jobs[1].LastError != "timeout" || jobs[0].State != "published" {
		t.Fatalf("%+v %v", jobs, err)
	}
}

func TestRuntimeReadOnlyViewWhileCoordinatorOpen(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	claimed, err := q.claimSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	view, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 64)
	if err != nil || len(view.Sources) != 1 || !view.Sources[0].Claimed {
		t.Fatalf("%+v %v", view, err)
	}
	if claimed.id != view.Sources[0].ID {
		t.Fatal("claim changed")
	}
	if _, err = ReadRuntime(t.Context(), dir, "wrong", 0, 64); err == nil {
		t.Fatal("wrong archive allowed")
	}
}

func TestRuntimeUnchangedCapturePreservesByteWatermark(t *testing.T) {
	dir, o, req := setup(t)
	now := time.Unix(100, 0)
	o.Now = func() time.Time { return now }
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	options := runtimeDefaults(RuntimeOptions{})
	if _, err := q.captureNext(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	first, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	req.ObservedAtNS = now.UnixNano()
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := q.captureNext(t.Context(), options); err != nil {
			t.Fatal(err)
		}
	}
	last, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || last[0].CapturedAtNS != first[0].CapturedAtNS || last[0].CapturedVersion != 2 || last[0].ObservedAtNS != req.ObservedAtNS {
		t.Fatalf("first=%+v last=%+v err=%v", first, last, err)
	}
}

func TestRuntimeMalformedManifestIsolatesSession(t *testing.T) {
	dir, o, a := setup(t)
	q := open(t, dir, o)
	capture(t, q, a)
	b := a
	b.Identity.SessionID = "healthy"
	capture(t, q, b)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), "UPDATE jobs SET manifest='invalid' WHERE session=?", a.Identity.BaseKey()); err != nil {
		t.Fatal(err)
	}
	if did, err := q.deliveryNext(t.Context(), newBackend(t), runtimeDefaults(RuntimeOptions{})); err != nil || !did {
		t.Fatalf("did=%v err=%v", did, err)
	}
	if _, err := q.deliveryNext(t.Context(), newBackend(t), runtimeDefaults(RuntimeOptions{})); err != nil {
		t.Fatal(err)
	}
	jobs, err := q.Status(t.Context())
	if err == nil {
		t.Fatal("corrupt manifest should remain explicit in legacy status")
	}
	_ = jobs
	quarantines, err := q.Quarantine(t.Context(), 0, 64)
	if err != nil || len(quarantines) != 1 || quarantines[0].Reason != "integrity-error" || quarantines[0].Session != a.Identity.BaseKey() {
		t.Fatalf("%+v %v", quarantines, err)
	}
	var state string
	if err := q.db.QueryRowContext(t.Context(), "SELECT state FROM jobs WHERE session=?", b.Identity.BaseKey()).Scan(&state); err != nil || state != "published" {
		t.Fatalf("healthy state=%s err=%v", state, err)
	}
}

func TestRuntimeInboxPoisonDoesNotBlockAcceptedHint(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := WriteHint(t.Context(), dir, req); err != nil {
		t.Fatal(err)
	}
	// More than one old scheduler page of invalid files must not be a barrier.
	for n := range 70 {
		path := filepath.Join(dir, "hints", fmt.Sprintf("%064d.json", n))
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 1 {
		t.Fatalf("%+v %v", sources, err)
	}
	// A parseable but forged filename must not overwrite the valid source hint.
	req.Path += ".forged"
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hints", strings.Repeat("f", 64)+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	sources, err = q.WorkStatus(t.Context(), 0, 64)
	if err != nil || sources[0].ObservedVersion != 1 {
		t.Fatalf("%+v %v", sources, err)
	}
}

func TestRuntimeMetadataLimitStillAllowsCoalescing(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// Fill in one local metadata transaction, without thousands of fsyncs.
	_, err := q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO runtime_sources(session,source,request,observed_at) SELECT 'other',CAST(x AS TEXT),'{}',1 FROM n`, MaxRuntimeSources-1)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatalf("existing hint lost at capacity: %v", err)
	}
	req.SourceID = "new"
	if err := q.Hint(t.Context(), req); !errors.Is(err, ErrQuota) {
		t.Fatalf("unbounded metadata: %v", err)
	}
}

func TestRuntimeNewHintDuringCaptureIsNotAcknowledged(t *testing.T) {
	dir, o, req := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	o.Fault = func(stage string) error {
		if stage == "payload-before-sync" {
			close(entered)
			<-release
		}
		return nil
	}
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); done <- err }()
	<-entered
	if err := q.Hint(t.Context(), req); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	status, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || status[0].State != "pending" || status[0].ObservedVersion != 2 || status[0].CapturedVersion != 1 || status[0].Claimed {
		t.Fatalf("%+v %v", status, err)
	}
}

func TestRuntimeRetryBoundsAndSafeClasses(t *testing.T) {
	for _, code := range []FailureClass{FailureTransient, FailureDestination, FailurePolicy, FailureIntegrity, FailureQuota, FailureTimeout, FailureAbsent} {
		if got := classify(errors.New("private"), func(error) FailureClass { return code }); got != code {
			t.Fatalf("%s != %s", got, code)
		}
	}
	if got := classify(errors.New("private"), func(error) FailureClass { return "private-unknown" }); got != FailureTransient {
		t.Fatal(got)
	}
	for _, err := range []error{ErrDestination, ErrPolicy, ErrIntegrity, ErrQuota, context.DeadlineExceeded} {
		if classify(err, nil) == FailureTransient {
			t.Fatalf("unclassified %v", err)
		}
	}
	for _, attempt := range []int{-1, 0, 1, 10, 1000} {
		for _, jitter := range []func(time.Duration) time.Duration{nil, func(time.Duration) time.Duration { return -1 }, func(time.Duration) time.Duration { return time.Hour }} {
			if delay := retryDelay(attempt, jitter); delay < time.Second || delay > 5*time.Minute {
				t.Fatal(delay)
			}
		}
	}
	defaults := runtimeDefaults(RuntimeOptions{Workers: 100})
	if defaults.CaptureWorkers != 64 || defaults.DeliveryWorkers != 64 || defaults.MaxAttempts != 8 || defaults.PollInterval != 30*time.Second {
		t.Fatalf("%+v", defaults)
	}
}

func TestRuntimeWriteHintBoundsAndUnsafeInbox(t *testing.T) {
	dir, _, req := setup(t)
	invalid := req
	invalid.ObservedAtNS = -1
	if err := WriteHint(t.Context(), dir, invalid); err == nil {
		t.Fatal("invalid hint accepted")
	}
	invalid = req
	invalid.Path = strings.Repeat("x", MaxHintBytes)
	if err := WriteHint(t.Context(), dir, invalid); !errors.Is(err, ErrQuota) {
		t.Fatalf("unbounded hint: %v", err)
	}
	unsafe := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteHint(t.Context(), unsafe, req); err == nil {
		t.Fatal("unsafe spool accepted")
	}
	if err := WriteHint(t.Context(), dir, req); err != nil {
		t.Fatal(err)
	}
	for n := range MaxInboxHints - 1 {
		if err := os.WriteFile(filepath.Join(dir, "hints", fmt.Sprintf("junk-%d", n)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteHint(t.Context(), dir, req); err != nil {
		t.Fatalf("coalescing refused at capacity: %v", err)
	}
	req.SourceID = "another"
	if err := WriteHint(t.Context(), dir, req); !errors.Is(err, ErrQuota) {
		t.Fatalf("inbox grew past quota: %v", err)
	}
}
