package sessioncapture

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// hangBudget bounds a delivery by counting steps, never by wall time: every
// ctx.Err call spends one unit and every fault boundary spends one unit.
// Exhaustion cancels the context, so a loop that makes no progress ends with
// context.Canceled instead of hanging the package until its test timeout.
type hangBudget struct {
	context.Context
	cancel context.CancelFunc
	errs   atomic.Int64
	hits   atomic.Int64
	only   string // boundary that fails with failWith ("" = none)
	fails  error
	onlyN  atomic.Int64
	limit  int64
}

func newHangBudget(t *testing.T, limit int64) *hangBudget {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	return &hangBudget{Context: ctx, cancel: cancel, limit: limit}
}

func (b *hangBudget) Err() error {
	if b.errs.Add(1) > b.limit {
		b.cancel()
	}
	return b.Context.Err()
}

func (b *hangBudget) fault(boundary string) error {
	if b.hits.Add(1) > b.limit {
		b.cancel()
	}
	if b.only != "" && boundary == b.only {
		if b.onlyN.Add(1) >= 2 {
			b.cancel() // a second attempt means the failure was retried in place
		}
		return b.fails
	}
	return nil
}

// Declared first on purpose: a mutant that leaks the result set pins the only
// connection, and every later database test would block instead of failing.
func TestHangAddColumnIfMissingAddsOnceAndReportsErrors(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if _, err := q.db.ExecContext(t.Context(), `CREATE TABLE hang_probe(a INTEGER)`); err != nil {
		t.Fatal(err)
	}
	columns := func() []string {
		rows, err := q.db.QueryContext(t.Context(), `PRAGMA table_info(hang_probe)`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var names []string
		for rows.Next() {
			var cid, notnull, pk int
			var name, kind string
			var def *string
			if err = rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return names
	}
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if err := q.addColumnIfMissing(gone, "hang_probe", "b", "TEXT NOT NULL DEFAULT 'x'"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled add: %v", err)
	}
	for range 2 {
		if err := q.addColumnIfMissing(t.Context(), "hang_probe", "b", "TEXT NOT NULL DEFAULT 'x'"); err != nil {
			t.Fatal(err)
		}
		// A leaked result set would pin the only connection and block every later query.
		if n := q.db.Stats().InUse; n != 0 {
			t.Fatalf("addColumnIfMissing left %d connections in use", n)
		}
		if got := columns(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("columns %v, want [a b]", got)
		}
	}
}

// Normal delivery of a multi-source session (raw lane then optional lane) must
// publish every job and release both lane locks within a small step budget.
func TestHangDeliveryTerminatesAndPublishes(t *testing.T) {
	budget := newHangBudget(t, 5000)
	f := newDeliveryMutationFixture(t, budget.fault)
	budget.hits.Store(0)
	if err := f.q.Deliver(budget, f.b, f.m.Identity); err != nil {
		t.Fatalf("Deliver: %v (err calls %d, fault hits %d)", err, budget.errs.Load(), budget.hits.Load())
	}
	st, err := f.q.Status(t.Context())
	if err != nil || len(st) == 0 {
		t.Fatalf("status %v %v", st, err)
	}
	for n, s := range st {
		last := n == len(st)-1
		if (last && s.State != "published") || (!last && s.State != "published" && s.State != "superseded") {
			t.Fatalf("sequence %d state %q (last=%v), want published or superseded", s.Sequence, s.State, last)
		}
	}
	for _, optional := range []bool{false, true} {
		lock := f.q.deliveryLane(f.m.Identity, optional)
		if !lock.TryLock() {
			t.Fatalf("lane optional=%v lock leaked", optional)
		}
		lock.Unlock()
	}
}

// A persistent delivery failure is reported once to the caller; it is not
// retried inside Deliver. The second attempt would cancel the budget.
func TestHangPersistentFailureReturnsAfterOneAttempt(t *testing.T) {
	sentinel := errors.New("synthetic persistent upload failure")
	budget := newHangBudget(t, 5000)
	f := newDeliveryMutationFixture(t, budget.fault)
	budget.only, budget.fails = "upload-before", sentinel
	budget.onlyN.Store(0)
	err := f.q.Deliver(budget, f.b, f.m.Identity)
	if !errors.Is(err, sentinel) || errors.Is(err, context.Canceled) {
		t.Fatalf("Deliver: %v, want exactly the injected failure", err)
	}
	if n := budget.onlyN.Load(); n != 1 {
		t.Fatalf("failing boundary hit %d times, want 1", n)
	}
}

// Optional-repair admission exhaustion ends delivery with a visible terminal
// quota gap while raw delivery still completes, within the step budget.
func TestHangOptionalQuotaTerminatesWithGap(t *testing.T) {
	budget := newHangBudget(t, 5000)
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	o.Fault = budget.fault
	q := open(t, dir, o)
	capture(t, q, r)
	enrichOnce(t, q, snapshotOptions("optional body"))
	if _, err := q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO optional_repairs(id,session,source_hash,epoch,commitment,source,evidence,identity,state,stage,contract,software) SELECT 'full-'||x,'quota','hash','epoch','commitment','{}','{}','{}','quarantined','quota','contract','test' FROM n`, MaxOptionalRepairRecords); err != nil {
		t.Fatal(err)
	}
	backend := newBackend(t)
	for n := range 3 {
		if err := q.Deliver(budget, backend, r.Identity); err != nil {
			t.Fatalf("Deliver %d: %v", n, err)
		}
	}
	st, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	gap := false
	for _, s := range st {
		gap = gap || s.OptionalRepairGap == "quota"
	}
	if !gap {
		t.Fatalf("terminal quota gap not recorded: %+v", st)
	}
}

func TestHangClaimAndCaptureSteps(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if did, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil || did {
		t.Fatalf("empty captureNext: %v %v", did, err)
	}
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if did, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil || !did {
		t.Fatalf("captureNext: %v %v", did, err)
	}
	if s := rowOf(t, q, r); s.state != "captured" {
		t.Fatalf("source row %+v", s)
	}
	if did, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil || did {
		t.Fatalf("settled captureNext: %v %v", did, err)
	}
}

func TestHangClaimSessionAndDeliveryLaneSteps(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	capture(t, q, r)
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if id, _, err := q.claimSession(gone, false); id != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled claimSession: %d %v", id, err)
	}
	id, identity, err := q.claimSession(t.Context(), false)
	if err != nil || id == 0 || identity != r.Identity {
		t.Fatalf("claimSession: %d %+v %v", id, identity, err)
	}
	if again, _, err := q.claimSession(t.Context(), false); err != nil || again != 0 {
		t.Fatalf("claimed session offered twice: %d %v", again, err)
	}
	q.releaseSessionClaim(t.Context(), id)
	did, err := q.deliveryLaneNext(t.Context(), newBackend(t), runtimeDefaults(RuntimeOptions{}), false)
	if err != nil || !did {
		t.Fatalf("deliveryLaneNext: %v %v", did, err)
	}
	st, err := q.Status(t.Context())
	if err != nil || len(st) != 1 || st[0].State != "published" {
		t.Fatalf("status %+v %v", st, err)
	}
	if did, err = q.deliveryLaneNext(t.Context(), newBackend(t), runtimeDefaults(RuntimeOptions{}), false); err != nil || did {
		t.Fatalf("drained deliveryLaneNext: %v %v", did, err)
	}
}

func TestHangMalformedHeadQuarantinedOnce(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := capture(t, q, r)
	session := r.Identity.BaseKey()
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if err := q.quarantineMalformedJob(gone, session, m.Sequence, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled quarantine: %v", err)
	}
	var state string
	if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE session=?`, session).Scan(&state); err != nil || state == "quarantined" {
		t.Fatalf("failed quarantine changed job: %q %v", state, err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET manifest=? WHERE session=?`, []byte("{}"), session); err != nil {
		t.Fatal(err)
	}
	id, due, err := q.dueIdentity(t.Context(), session)
	if err != nil || due || id != (publication.Identity{}) {
		t.Fatalf("dueIdentity: %+v %v %v", id, due, err)
	}
	var lastError, reason, kind string
	var rows int
	if err = q.db.QueryRowContext(t.Context(), `SELECT state,last_error FROM jobs WHERE session=?`, session).Scan(&state, &lastError); err != nil {
		t.Fatal(err)
	}
	if err = q.db.QueryRowContext(t.Context(), `SELECT count(*),min(reason),min(kind) FROM runtime_quarantine WHERE session=? AND sequence=?`, session, m.Sequence).Scan(&rows, &reason, &kind); err != nil {
		t.Fatal(err)
	}
	if state != "quarantined" || lastError != "integrity-error" || rows != 1 || reason != string(FailureIntegrity) || kind != "delivery" {
		t.Fatalf("state %q last_error %q rows %d reason %q kind %q", state, lastError, rows, reason, kind)
	}
}
