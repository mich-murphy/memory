package sessioncapture

import (
	"context"
	"crypto/sha256"
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
	t.Parallel()
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
		// The stuck job is proven subsumed by the newer capture, so it is
		// superseded rather than delivered; the newer job must not be blocked.
		if s := jobStatus(t, q, r.Identity, 1); s.State != "superseded" {
			t.Fatalf("job 1 state=%s, want superseded", s.State)
		}
		if s := jobStatus(t, q, r.Identity, 2); s.State != "published" {
			t.Fatalf("job 2 state=%s, want published", s.State)
		}
	})
	t.Run("newer job is not blocked by a retrying job that cannot be superseded", func(t *testing.T) {
		q, b, clock, r := newLivenessQueue(t)
		capture(t, q, r)
		b.fail = func(string) bool { return true }
		for range 12 {
			livenessTick(q, b, clock, r.Identity)
		}
		// An enrichment gap makes job 1 ineligible for supersession, so the
		// original in-order-retry guarantee is exercised.
		if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET enrichment_gap='quota' WHERE session=? AND sequence=1`, r.Identity.BaseKey()); err != nil {
			t.Fatal(err)
		}
		appendFile(t, r.Path, []byte("more\n"))
		capture(t, q, r)
		b.fail = nil
		for range 4 {
			livenessTick(q, b, clock, r.Identity)
		}
		for seq := uint64(1); seq <= 2; seq++ {
			if s := jobStatus(t, q, r.Identity, seq); s.State != "published" {
				t.Fatalf("job %d state=%s, want published", seq, s.State)
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
	t.Parallel()
	const limit = 32
	dir, o, r := setup(t)
	o.MaxSpoolBytes = 64 << 20
	clock := time.Unix(1000, 0)
	o.Now = func() time.Time { return clock }
	q := open(t, dir, o)
	if err := q.setSourceLimit(limit); err != nil {
		t.Fatal(err)
	}
	var last publication.Manifest
	var capErr error
	for i := 1; i <= limit+6; i++ {
		write(t, r.Path, fmt.Sprintf("%08d\n", i))
		m, err := q.Capture(t.Context(), r)
		if err != nil {
			capErr = err
			break
		}
		last = m
	}
	if len(last.Sources) != limit {
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
	// Read only the last accepted job's manifest rather than parsing every job.
	var body []byte
	if err := q.db.QueryRowContext(t.Context(), `SELECT manifest FROM jobs WHERE session=? AND sequence=?`, r.Identity.BaseKey(), last.Sequence).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var stored publication.Manifest
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Identity != r.Identity || stored.Sequence != last.Sequence || stored.PublicationID != last.PublicationID || len(stored.Sources) != limit {
		t.Fatalf("last publication changed: %s with %d sources", stored.PublicationID, len(stored.Sources))
	}
}

func TestSourceLimitDefaultAndBounds(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if got := q.effectiveSourceLimit(); got != publication.MaxSources {
		t.Fatalf("default limit %d, want %d", got, publication.MaxSources)
	}
	for _, bad := range []int{-1, publication.MaxSources + 1} {
		if err := q.setSourceLimit(bad); !errors.Is(err, errSourceLimitBounds) {
			t.Fatalf("setSourceLimit(%d) = %v", bad, err)
		}
		if got := q.effectiveSourceLimit(); got != publication.MaxSources {
			t.Fatalf("rejected %d changed limit to %d", bad, got)
		}
	}
	for _, ok := range []int{1, publication.MaxSources} {
		if err := q.setSourceLimit(ok); err != nil || q.effectiveSourceLimit() != ok {
			t.Fatalf("setSourceLimit(%d) = %v, effective %d", ok, err, q.effectiveSourceLimit())
		}
	}
	if err := q.setSourceLimit(0); err != nil || q.effectiveSourceLimit() != publication.MaxSources {
		t.Fatalf("zero must restore the default: %v %d", err, q.effectiveSourceLimit())
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
	t.Parallel()
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

func TestRunnableSourceIndexMatchesClaimQuery(t *testing.T) {
	plan := func(q *Queue) string {
		rows, err := q.db.QueryContext(t.Context(), `EXPLAIN QUERY PLAN `+claimSourceSQL, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "\n")
	}
	indexes := func(q *Queue) string {
		rows, err := q.db.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='index' AND name LIKE 'runtime_runnable_sources%' ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			names = append(names, n)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(names, ",")
	}
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	// A queue created by an older build carries the stale three-state index.
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := indexes(q); got != "runtime_runnable_sources_v2" {
		t.Fatalf("fresh indexes %q", got)
	}
	if _, err := q.db.ExecContext(t.Context(), `DROP INDEX runtime_runnable_sources_v2`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `CREATE INDEX runtime_runnable_sources ON runtime_sources(id) WHERE claim=0 AND state NOT IN ('absent','captured','quarantined')`); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := indexes(q); got != "runtime_runnable_sources_v2" {
		t.Fatalf("reopened indexes %q", got)
	}
	if got := plan(q); !strings.Contains(got, "USING INDEX runtime_runnable_sources_v2") {
		t.Fatalf("claim plan %q", got)
	}
}

// throttledBackend delays object uploads to bps bytes per second.
type throttledBackend struct {
	publication.Backend
	bps int
}

// TransferBudget stands in for the R2 backend's size-aware per-object allowance.
func (b throttledBackend) TransferBudget(int64) time.Duration { return 10 * time.Second }

func (b throttledBackend) Sync(ctx context.Context, key string) error {
	if d, ok := b.Backend.(publication.DurabilityFence); ok {
		return d.Sync(ctx, key)
	}
	return nil
}

func (b throttledBackend) PutFile(ctx context.Context, key, path string) error {
	if strings.Contains(key, "/objects/") {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(n) * time.Second / time.Duration(b.bps)):
				}
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
	}
	return b.Backend.PutFile(ctx, key, path)
}

func runThrottledDelivery(t *testing.T, opTimeout time.Duration) Status {
	t.Helper()
	dir, o, r := setup(t)
	o.MaxSpoolBytes = 1 << 26
	o.Now = time.Now
	q := open(t, dir, o)
	write(t, r.Path, string(incompressible(100_000)))
	b := throttledBackend{Backend: newBackend(t), bps: 100_000} // About 1s per upload attempt.
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- q.Run(ctx, b, RuntimeOptions{OperationTimeout: opTimeout, PollInterval: 50 * time.Millisecond, Workers: 1, Jitter: func(time.Duration) time.Duration { return time.Second }})
	}()
	var s Status
	for ctx.Err() == nil {
		if all, _ := q.Status(ctx); len(all) == 1 {
			s = all[0]
			if s.State == "published" || s.State == "quarantined" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	<-done
	return s
}

func TestObjectLongerThanOperationBudgetPublishes(t *testing.T) {
	// Each upload takes about a second against a 200ms operation budget; the
	// backend's TransferBudget extends the attempt, and the backend owns the bound.
	s := runThrottledDelivery(t, 200*time.Millisecond)
	if s.State != "published" || s.Attempts != 1 {
		t.Fatalf("state=%s attempts=%d last_error=%s", s.State, s.Attempts, s.LastError)
	}
}

func incompressible(n int) []byte {
	out := make([]byte, 0, n+sha256.Size)
	block := sha256.Sum256([]byte("l1-incompressible"))
	for len(out) < n {
		out = append(out, block[:]...)
		block = sha256.Sum256(block[:])
	}
	return out[:n]
}

// slowObjectBackend takes each upload 600ms and reports a 1s allowance per object.
type slowObjectBackend struct {
	publication.Backend
	uploads int
}

func (b *slowObjectBackend) TransferBudget(int64) time.Duration { return time.Second }

func (b *slowObjectBackend) PutFile(ctx context.Context, key, path string) error {
	if strings.Contains(key, "/objects/") {
		b.uploads++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(600 * time.Millisecond):
		}
	}
	return b.Backend.PutFile(ctx, key, path)
}

func TestTransferAllowanceSumsSelectedObjects(t *testing.T) {
	b := &slowObjectBackend{}
	srcs := []publication.Source{
		{Object: publication.Object{EncodedSize: 1}},
		{Object: publication.Object{EncodedSize: 2}},
		{Withheld: &publication.WithheldSource{}},
	}
	if got := transferAllowance(b, srcs...); got != 2*time.Second {
		t.Fatalf("allowance %s", got)
	}
	if got := transferAllowance(newBackend(t), srcs...); got != 0 {
		t.Fatalf("backend without budget: %s", got)
	}
}

func TestManifestOfSlowObjectsPublishesInOneStep(t *testing.T) {
	t.Parallel()
	dir, o, r := setup(t)
	q := open(t, dir, o)
	for i, name := range []string{"a", "b", "c"} {
		req := r
		req.SourceID = "source-" + name
		req.Path = filepath.Join(filepath.Dir(r.Path), name+".jsonl")
		write(t, req.Path, fmt.Sprintf("object %d\n", i))
		capture(t, q, req)
	}
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Only the newest job (three sources) stays pending, so one step moves all three.
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state='superseded' WHERE sequence<(SELECT MAX(sequence) FROM jobs)`); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := q.db.QueryRowContext(t.Context(), `SELECT manifest FROM jobs WHERE state='pending'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var head3 publication.Manifest
	if err := json.Unmarshal(raw, &head3); err != nil {
		t.Fatal(err)
	}
	selected := 0
	for i := range head3.Sources {
		if publication.IsSelected(head3.Sources[i]) {
			selected++
		}
	}
	if selected != 3 {
		t.Fatalf("head manifest selects %d objects, want 3", selected)
	}
	b := &slowObjectBackend{Backend: newBackend(t)}
	// 3 x 600ms of upload exceeds one object's 1s allowance and a flat 1.2s bound.
	opts := runtimeDefaults(RuntimeOptions{OperationTimeout: 200 * time.Millisecond})
	opts.budgeted = true // as Run sets it for a backend that reports budgets
	step, cancelStep := stepContext(t.Context(), "delivery", opts)
	defer cancelStep()
	if did, err := q.deliverDueWork(step, b, r.Identity, opts); err != nil || !did {
		t.Fatalf("did=%v err=%v", did, err)
	}
	all, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var head Status
	for i := range all {
		if all[i].Sequence > head.Sequence {
			head = all[i]
		}
	}
	if head.State != "published" || head.Attempts != 1 || b.uploads != 3 {
		t.Fatalf("state=%s attempts=%d last=%s uploads=%d", head.State, head.Attempts, head.LastError, b.uploads)
	}
}

// With the default jitter (nil) the delay is drawn from [d/2, d]. 64 draws of a
// 32s base span about 16e9 ns values, so the chance that all draws coincide
// (jitter not applied) is below 1e-600 in practice; no sleeping is involved.
func TestDefaultRetryJitterIsAppliedWithinBounds(t *testing.T) {
	const base = 32 * time.Second
	seen := map[time.Duration]bool{}
	for range 64 {
		got := retryDelay(6, nil)
		if got < base/2 || got > base {
			t.Fatalf("retryDelay(6, nil)=%v outside [%v,%v]", got, base/2, base)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatalf("jitter not applied: %d distinct values in 64 draws", len(seen))
	}
}
