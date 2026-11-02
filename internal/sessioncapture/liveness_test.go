package sessioncapture

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// flakyBackend fails every upload or conditional write whose key matches fail
// with a plain transport error, as a network flap would.
type flakyBackend struct {
	publication.Backend
	fail func(key string) bool
}

func (b *flakyBackend) Sync(ctx context.Context, key string) error {
	if d, ok := b.Backend.(publication.DurabilityFence); ok {
		return d.Sync(ctx, key)
	}
	return nil
}

func (b *flakyBackend) PutFile(ctx context.Context, key, path string) error {
	if b.fail != nil && b.fail(key) {
		return errors.New("put r2: connection reset by peer")
	}
	return b.Backend.PutFile(ctx, key, path)
}

func (b *flakyBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	if b.fail != nil && b.fail(key) {
		return errors.New("cas r2: connection reset by peer")
	}
	return b.Backend.CompareAndSwap(ctx, key, token, body)
}

func jobStatus(t *testing.T, q *Queue, id publication.Identity, seq uint64) Status {
	t.Helper()
	all, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := range all {
		if all[i].Identity == id && all[i].Sequence == seq {
			return all[i]
		}
	}
	t.Fatalf("no job %d for %v", seq, id)
	return Status{}
}

func appendFile(t *testing.T, p string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(b); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func newLivenessQueue(t *testing.T) (*Queue, *flakyBackend, *time.Time, Request) {
	t.Helper()
	dir, o, r := setup(t)
	clock := time.Unix(1000, 0)
	o.Now = func() time.Time { return clock }
	return open(t, dir, o), &flakyBackend{Backend: newBackend(t)}, &clock, r
}

// livenessTick attempts one due delivery, then lets the longest backoff elapse.
func livenessTick(q *Queue, b publication.Backend, clock *time.Time, id publication.Identity) {
	_ = q.DeliverDue(context.Background(), b, id)
	*clock = clock.Add(10 * time.Minute)
}

func TestTransientDeliveryFailureNeverQuarantines(t *testing.T) {
	t.Run("one failure then heal", func(t *testing.T) {
		q, b, clock, r := newLivenessQueue(t)
		capture(t, q, r)
		b.fail = func(string) bool { return true }
		livenessTick(q, b, clock, r.Identity)
		if s := jobStatus(t, q, r.Identity, 1); s.State != "retry" || s.Attempts != 1 {
			t.Fatalf("state=%s attempts=%d", s.State, s.Attempts)
		}
		b.fail = nil
		livenessTick(q, b, clock, r.Identity)
		if s := jobStatus(t, q, r.Identity, 1); s.State != "published" {
			t.Fatalf("state=%s", s.State)
		}
	})
	t.Run("stays retrying beyond MaxAttempts", func(t *testing.T) {
		q, b, clock, r := newLivenessQueue(t)
		capture(t, q, r)
		b.fail = func(string) bool { return true }
		for i := 1; i <= 12; i++ {
			livenessTick(q, b, clock, r.Identity)
			s := jobStatus(t, q, r.Identity, 1)
			if s.State != "retry" || s.Attempts != i || s.LastError != "transient" {
				t.Fatalf("tick %d: state=%s attempts=%d last_error=%s", i, s.State, s.Attempts, s.LastError)
			}
		}
	})
	t.Run("publishes after heal without replay", func(t *testing.T) {
		q, b, clock, r := newLivenessQueue(t)
		capture(t, q, r)
		b.fail = func(string) bool { return true }
		for range 12 {
			livenessTick(q, b, clock, r.Identity)
		}
		b.fail = nil
		for range 3 {
			livenessTick(q, b, clock, r.Identity)
		}
		if s := jobStatus(t, q, r.Identity, 1); s.State != "published" {
			t.Fatalf("state=%s", s.State)
		}
	})
	t.Run("newer job is not blocked", func(t *testing.T) {
		q, b, clock, r := newLivenessQueue(t)
		capture(t, q, r)
		b.fail = func(string) bool { return true }
		for range 12 {
			livenessTick(q, b, clock, r.Identity)
		}
		appendFile(t, r.Path, []byte("more\n"))
		capture(t, q, r)
		b.fail = nil
		for range 4 {
			livenessTick(q, b, clock, r.Identity)
		}
		for seq := uint64(1); seq <= 2; seq++ {
			if s := jobStatus(t, q, r.Identity, seq); s.State != "published" {
				t.Fatalf("job %d state=%s", seq, s.State)
			}
		}
	})
	t.Run("other session is isolated", func(t *testing.T) {
		q, b, clock, r := newLivenessQueue(t)
		other := r
		other.Identity.SessionID = "other"
		other.SourceID = "source-b"
		other.Path = filepath.Join(filepath.Dir(r.Path), "b.jsonl")
		write(t, other.Path, "b\n")
		capture(t, q, r)
		capture(t, q, other)
		hexA := "/sessions/" + hex.EncodeToString([]byte("session")) + "/"
		b.fail = func(k string) bool { return strings.Contains(k, hexA) }
		for range 12 {
			livenessTick(q, b, clock, r.Identity)
			livenessTick(q, b, clock, other.Identity)
		}
		if s := jobStatus(t, q, other.Identity, 1); s.State != "published" {
			t.Fatalf("session B state=%s", s.State)
		}
		if s := jobStatus(t, q, r.Identity, 1); s.State != "retry" {
			t.Fatalf("session A state=%s", s.State)
		}
	})
}

func TestRetryDelayExactTable(t *testing.T) {
	identity := func(d time.Duration) time.Duration { return d }
	s := time.Second
	want := map[int]time.Duration{-5: s, -1: s, 0: s, 1: s, 2: 2 * s, 3: 4 * s, 4: 8 * s, 5: 16 * s, 6: 32 * s, 7: 64 * s, 8: 128 * s, 9: 256 * s, 10: 300 * s, 11: 300 * s, 1000: 300 * s}
	for a, w := range want {
		if got := retryDelay(a, identity); got != w {
			t.Errorf("retryDelay(%d, identity)=%v want %v", a, got, w)
		}
		lo := max(w/2, s)
		for range 50 {
			if got := retryDelay(a, nil); got < lo || got > w {
				t.Fatalf("retryDelay(%d, nil)=%v want [%v,%v]", a, got, lo, w)
			}
		}
	}
}

func TestFailureClassesAndQuarantineBudget(t *testing.T) {
	for _, tc := range []struct {
		err    error
		class  FailureClass
		budget bool
	}{
		{errors.New("connection reset"), FailureTransient, false},
		{context.DeadlineExceeded, FailureTimeout, false},
		{ErrDestination, FailureDestination, false},
		{ErrIntegrity, FailureIntegrity, true},
		{ErrPolicy, FailurePolicy, true},
		{ErrQuota, FailureQuota, true},
		{publication.ErrConflict, FailureConflict, true},
		{fmt.Errorf("wrapped: %w", publication.ErrRejected), FailureRejected, true},
	} {
		got := classify(tc.err, nil)
		if got != tc.class || got.spendsQuarantineBudget() != tc.budget {
			t.Errorf("%v: class=%s budget=%v want %s/%v", tc.err, got, got.spendsQuarantineBudget(), tc.class, tc.budget)
		}
	}
	if FailureAbsent.spendsQuarantineBudget() || FailureClass("unknown").spendsQuarantineBudget() {
		t.Error("absent and unknown classes must not consume the budget")
	}
}

func TestConflictQuarantinesAsVisibleClass(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, r)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{MaxAttempts: 2})
	if err := q.persistDeliveryFailure(t.Context(), r.Identity, m.Sequence, 1, publication.ErrConflict, opts, false); err != nil {
		t.Fatal(err)
	}
	if s := jobStatus(t, q, r.Identity, m.Sequence); s.State != "retry" || s.LastError != "conflict" || s.Attempts != 0 {
		t.Fatalf("first conflict: %+v", s)
	}
	if err := q.persistDeliveryFailure(t.Context(), r.Identity, m.Sequence, 2, publication.ErrConflict, opts, false); err != nil {
		t.Fatal(err)
	}
	entries, err := q.Quarantine(t.Context(), 0, 8)
	if err != nil || len(entries) != 1 || entries[0].Reason != "conflict" || entries[0].Attempts != 2 || !entries[0].Active {
		t.Fatalf("%+v %v", entries, err)
	}
	if s := jobStatus(t, q, r.Identity, m.Sequence); s.State != "quarantined" {
		t.Fatalf("state %s", s.State)
	}
}

func TestPendingLagIsVisibleInStatus(t *testing.T) {
	q, b, clock, r := newLivenessQueue(t)
	m := capture(t, q, r)
	b.fail = func(string) bool { return true }
	for range 3 {
		livenessTick(q, b, clock, r.Identity)
	}
	s := jobStatus(t, q, r.Identity, 1)
	if s.Attempts != 3 || s.LastError != "transient" || s.CapturedAtNS != m.CapturedAtNS || m.CapturedAtNS == 0 {
		t.Fatalf("%+v captured=%d", s, m.CapturedAtNS)
	}
	rs, err := q.RuntimeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rs.PendingPublications != 1 || rs.RetryingPublications != 1 || rs.MaxPendingAttempts != 3 || rs.OldestPendingPublicationAtNS != m.CapturedAtNS || rs.Quarantined != 0 {
		t.Fatalf("%+v", rs)
	}
	b.fail = nil
	livenessTick(q, b, clock, r.Identity)
	if rs, err = q.RuntimeStatus(t.Context()); err != nil || rs.PendingPublications != 0 || rs.RetryingPublications != 0 || rs.MaxPendingAttempts != 0 || rs.OldestPendingPublicationAtNS != 0 {
		t.Fatalf("%+v %v", rs, err)
	}
}

func TestTransientCaptureFailureNeverQuarantines(t *testing.T) {
	dir, o, r := setup(t)
	clock := time.Unix(1000, 0)
	o.Now = func() time.Time { return clock }
	failing := true
	o.Fault = func(point string) error {
		if failing && point == "payload-before-sync" {
			return errors.New("disk hiccup")
		}
		return nil
	}
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{})
	for i := 1; i <= 12; i++ {
		captureTurns(t, q, opts)
		clock = clock.Add(10 * time.Minute)
		src, err := q.WorkStatus(t.Context(), 0, 8)
		if err != nil || len(src) != 1 || src[0].State != "retry" || src[0].Attempts != i || src[0].LastError != "transient" {
			t.Fatalf("tick %d: %+v %v", i, src, err)
		}
	}
	failing = false
	captureTurns(t, q, opts)
	if src, err := q.WorkStatus(t.Context(), 0, 8); err != nil || src[0].State != "captured" {
		t.Fatalf("%+v %v", src, err)
	}
}

func TestSpoolFullRetriesPastMaxAttemptsThenCaptures(t *testing.T) {
	dir, o, r := setup(t)
	clock := time.Unix(1000, 0)
	o.Now = func() time.Time { return clock }
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	q.opts.MaxSpoolBytes = 1
	opts := runtimeDefaults(RuntimeOptions{MaxAttempts: 2})
	for i := 1; i <= 5; i++ {
		captureTurns(t, q, opts)
		clock = clock.Add(10 * time.Minute)
		src, err := q.WorkStatus(t.Context(), 0, 8)
		if err != nil || len(src) != 1 || src[0].State != "retry" || src[0].Attempts != i || src[0].LastError != "spool-full" {
			t.Fatalf("attempt %d: %+v %v", i, src, err)
		}
	}
	rs, err := q.RuntimeStatus(t.Context())
	if err != nil || rs.SpoolFullWork != 1 || rs.Quarantined != 0 {
		t.Fatalf("%+v %v", rs, err)
	}
	q.opts.MaxSpoolBytes = 1 << 20
	captureTurns(t, q, opts)
	if src, err := q.WorkStatus(t.Context(), 0, 8); err != nil || src[0].State != "captured" {
		t.Fatalf("%+v %v", src, err)
	}
}

func TestPermanentQuotaQuarantinesWithTypedReason(t *testing.T) {
	dir, o, r := setup(t)
	o.Fault = func(point string) error {
		if point == "payload-before-sync" {
			return ErrQuota
		}
		return nil
	}
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	captureTurns(t, q, runtimeDefaults(RuntimeOptions{MaxAttempts: 1}))
	entries, err := q.Quarantine(t.Context(), 0, 8)
	if err != nil || len(entries) != 1 || entries[0].Kind != "capture" || entries[0].Reason != "quota" || entries[0].Attempts != 1 {
		t.Fatalf("%+v %v", entries, err)
	}
}

// lostACKBackend applies the first guarded write, then reports a conflict, as a
// lost acknowledgement followed by a retried precondition would.
type lostACKBackend struct {
	publication.Backend
	lost bool
}

func (b *lostACKBackend) Sync(ctx context.Context, key string) error {
	if d, ok := b.Backend.(publication.DurabilityFence); ok {
		return d.Sync(ctx, key)
	}
	return nil
}

func (b *lostACKBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	if err := b.Backend.CompareAndSwap(ctx, key, token, body); err != nil {
		return err
	}
	if !b.lost {
		b.lost = true
		return publication.ErrConflict
	}
	return nil
}

func TestLostACKConflictResolvesAsPublished(t *testing.T) {
	dir, o, r := setup(t)
	clock := time.Unix(1000, 0)
	o.Now = func() time.Time { return clock }
	q := open(t, dir, o)
	capture(t, q, r)
	b := &lostACKBackend{Backend: newBackend(t)}
	for range 3 {
		livenessTick(q, b, &clock, r.Identity)
	}
	if s := jobStatus(t, q, r.Identity, 1); s.State != "published" || s.LastError != "" {
		t.Fatalf("%+v", s)
	}
	if entries, err := q.Quarantine(t.Context(), 0, 8); err != nil || len(entries) != 0 {
		t.Fatalf("%+v %v", entries, err)
	}
}

func TestStatusQueryCostWithManyPendingJobs(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, r)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	const rows = 10000
	if _, err := q.db.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (SELECT 2 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO jobs(session,sequence,manifest,evidence,state,attempts) SELECT ?,i,?,'[]','retry',i%5 FROM n`, rows, m.Identity.BaseKey(), body); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rs, err := q.RuntimeStatus(t.Context())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RuntimeStatus over %d pending jobs: %v", rows, elapsed)
	if rs.PendingPublications != rows || rs.RetryingPublications != rows-1 || rs.MaxPendingAttempts != 4 || rs.OldestPendingPublicationAtNS != m.CapturedAtNS {
		t.Fatalf("%+v", rs)
	}
}

// captureTurns runs two scheduler turns: the capture cursor needs one idle turn
// to rotate back to a source it already visited.
func captureTurns(t *testing.T, q *Queue, o RuntimeOptions) {
	t.Helper()
	for range 2 {
		if _, err := q.captureNext(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSourceLimitIsTypedAndNotQuarantinedAsTransient(t *testing.T) {
	dir, o, r := setup(t)
	o.MaxSpoolBytes = 64 << 20
	clock := time.Unix(1000, 0)
	o.Now = func() time.Time { return clock }
	q := open(t, dir, o)
	var last publication.Manifest
	var capErr error
	for i := 1; i <= 1030; i++ {
		write(t, r.Path, fmt.Sprintf("%08d\n", i))
		m, err := q.Capture(t.Context(), r)
		if err != nil {
			capErr = err
			break
		}
		last = m
	}
	if len(last.Sources) != publication.MaxSources {
		t.Fatalf("last accepted manifest holds %d sources", len(last.Sources))
	}
	if !errors.Is(capErr, ErrSourceLimit) {
		t.Fatalf("capture error %v is not ErrSourceLimit", capErr)
	}
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{})
	for range 12 {
		captureTurns(t, q, opts)
		clock = clock.Add(10 * time.Minute)
	}
	var state, lastErr string
	var qn int
	if err := q.db.QueryRowContext(t.Context(), `SELECT state,last_error FROM runtime_sources LIMIT 1`).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_quarantine WHERE kind='capture'`).Scan(&qn); err != nil {
		t.Fatal(err)
	}
	if state != "source-limit" || lastErr != string(FailureSourceLimit) || qn != 0 {
		t.Fatalf("state=%s last_error=%s quarantine rows=%d", state, lastErr, qn)
	}
	rs, err := q.RuntimeStatus(t.Context())
	if err != nil || rs.SourceLimitSources != 1 || rs.Quarantined != 0 {
		t.Fatalf("%+v %v", rs, err)
	}
	// The previous successful publication stays accepted and unchanged.
	if jobs := jobStatus(t, q, r.Identity, last.Sequence); jobs.PublicationID != last.PublicationID {
		t.Fatalf("last publication changed: %+v", jobs)
	}
}

// optionalWork seeds one enrichment or optional-repair row at attempts=7 and
// returns the table, row id and a runner for one worker turn.
func optionalWork(t *testing.T, lane string, failure *error) (*Queue, string, string, func() error) {
	t.Helper()
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := capture(t, q, r)
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{MaxAttempts: 8, Enrich: func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		if *failure != nil {
			return EnrichmentArtifact{}, *failure
		}
		return EnrichmentArtifact{Body: io.NopCloser(strings.NewReader("ok")), Complete: true}, nil
	}})
	if lane == "enrichment" {
		spec := EnrichmentSpec{Kind: "snapshot", Identity: r.Identity, Source: SourceCoordinate{SourceID: r.SourceID, Path: r.Path}}
		body, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.db.ExecContext(t.Context(), `INSERT INTO enrichment_work(id,session,spec,contract,attempts) VALUES('w',?,?,?,7)`, r.Identity.BaseKey(), body, EnrichmentContract); err != nil {
			t.Fatal(err)
		}
		return q, "enrichment_work", "w", func() error { _, err := q.enrichmentNext(t.Context(), opts); return err }
	}
	if err := q.persistOptionalRepair(t.Context(), r.Identity, m.Sources[0], es[0], "upload"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE optional_repairs SET attempts=7`); err != nil {
		t.Fatal(err)
	}
	b := healingBackend{Backend: newBackend(t), failure: failure}
	return q, "optional_repairs", repairID(r.Identity, m.Sources[0]), func() error { _, err := q.optionalRepairNext(t.Context(), b, opts); return err }
}

func TestOptionalWorkFailureClassesFollowQuarantineBudget(t *testing.T) {
	for _, lane := range []string{"enrichment", "repair"} {
		t.Run(lane+"/transient retries past MaxAttempts and completes", func(t *testing.T) {
			failure := errors.New("connection reset")
			q, table, id, step := optionalWork(t, lane, &failure)
			if err := step(); err != nil {
				t.Fatal(err)
			}
			var state, last string
			var attempts int
			scan := func() {
				if err := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,last_error FROM `+table+` WHERE id=?`, id).Scan(&state, &attempts, &last); err != nil {
					t.Fatal(err)
				}
			}
			scan()
			if state != "retry" || attempts != 8 || last != "transient" {
				t.Fatalf("after outage: %s %d %s", state, attempts, last)
			}
			failure = nil
			if _, err := q.db.ExecContext(t.Context(), `UPDATE `+table+` SET next_retry=0`); err != nil {
				t.Fatal(err)
			}
			if err := step(); err != nil {
				t.Fatal(err)
			}
			scan()
			if state != "attached" && state != "repaired" {
				t.Fatalf("after heal: %s", state)
			}
		})
		t.Run(lane+"/integrity quarantines with its class", func(t *testing.T) {
			failure := fmt.Errorf("bad: %w", ErrIntegrity)
			q, table, id, step := optionalWork(t, lane, &failure)
			if err := step(); err != nil {
				t.Fatal(err)
			}
			var state, last string
			var attempts int
			if err := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,last_error FROM `+table+` WHERE id=?`, id).Scan(&state, &attempts, &last); err != nil {
				t.Fatal(err)
			}
			if state != "quarantined" || attempts != 8 || last != "integrity-error" {
				t.Fatalf("%s %d %s", state, attempts, last)
			}
		})
	}
}

// healingBackend fails uploads with *failure while it is set, then stores normally.
type healingBackend struct {
	publication.Backend
	failure *error
}

func (b healingBackend) PutFile(ctx context.Context, key, path string) error {
	if *b.failure != nil {
		return *b.failure
	}
	return b.Backend.PutFile(ctx, key, path)
}
