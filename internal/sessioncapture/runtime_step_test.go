package sessioncapture

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func captureRuntimeLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	old := runtimeLog
	buf := &bytes.Buffer{}
	runtimeLog = buf
	t.Cleanup(func() { runtimeLog = old })
	return buf
}

func TestTransientStepErrorClassification(t *testing.T) {
	ctx := t.Context()
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"deadline":        {context.DeadlineExceeded, true},
		"wrapped":         {fmt.Errorf("claim: %w", context.DeadlineExceeded), true},
		"joined deadline": {errors.Join(context.DeadlineExceeded, context.DeadlineExceeded), true},
		"durability":      {publication.ErrDurability, false},
		"deadline+fatal":  {errors.Join(context.DeadlineExceeded, publication.ErrDurability), false},
		"nested fatal":    {fmt.Errorf("x: %w", errors.Join(context.DeadlineExceeded, syscall.EIO)), false},
		"canceled":        {context.Canceled, false},
		"integrity":       {ErrIntegrity, false},
		"nil":             {nil, false},
	} {
		if got := transientStepError(ctx, false, c.err); got != c.want {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if transientStepError(done, false, context.DeadlineExceeded) {
		t.Fatal("deadline during Run shutdown must not be swallowed")
	}
}

func TestTransientStepErrorSQLiteBusy(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	ctx := t.Context()
	if _, err := q.db.ExecContext(ctx, "PRAGMA busy_timeout=1"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", filepath.Join(dir, "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	other.SetMaxOpenConns(1)
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "UPDATE binding SET archive=archive"); err != nil {
		t.Fatal(err)
	}
	_, busy := q.db.ExecContext(ctx, "UPDATE binding SET archive=archive")
	if busy == nil {
		t.Fatal("expected SQLITE_BUSY")
	}
	if !transientStepError(ctx, false, busy) {
		t.Fatalf("busy must be transient: %v", busy)
	}
	if transientStepError(ctx, false, errors.Join(busy, syscall.EIO)) {
		t.Fatal("busy joined with EIO must stay fatal")
	}
}

func TestRuntimeWorkerSkipsOperationDeadline(t *testing.T) {
	logs := captureRuntimeLog(t)
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	err := q.runtimeWorkerNamed(ctx, "probe", runtimeDefaults(RuntimeOptions{PollInterval: time.Millisecond}), func(context.Context) (bool, error) {
		calls++
		if calls == 3 {
			cancel()
			return false, nil
		}
		return true, fmt.Errorf("slow: %w", context.DeadlineExceeded)
	})
	if err != nil || calls < 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if !strings.Contains(logs.String(), "worker=probe") {
		t.Fatalf("missing log: %q", logs.String())
	}
	s, err := q.RuntimeStatus(t.Context())
	if err != nil || s.StepErrors < 1 || !strings.HasPrefix(s.LastStepError, "probe:") {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestRuntimeWorkerSurvivesConnectionStarvation(t *testing.T) {
	captureRuntimeLog(t)
	old := runtimeReleaseTimeout
	runtimeReleaseTimeout = 20 * time.Millisecond
	t.Cleanup(func() { runtimeReleaseTimeout = old })
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	held, err := q.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	opts := runtimeDefaults(RuntimeOptions{PollInterval: time.Millisecond, OperationTimeout: 15 * time.Millisecond})
	err = q.runtimeWorkerNamed(ctx, "capture", opts, func(c context.Context) (bool, error) {
		calls++
		switch calls {
		case 3:
			_ = held.Close() // starvation ends; the worker must still be alive
		case 5:
			cancel()
		}
		return q.captureNext(c, opts)
	})
	if err != nil || calls < 5 {
		t.Fatalf("Run-equivalent exited: err=%v calls=%d", err, calls)
	}
}

func TestRuntimeWorkerDurableErrorsStayFatal(t *testing.T) {
	captureRuntimeLog(t)
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	opts := runtimeDefaults(RuntimeOptions{PollInterval: time.Millisecond})
	for name, cause := range map[string]error{
		"durability": publication.ErrDurability,
		"joined":     fmt.Errorf("step: %w", errors.Join(context.DeadlineExceeded, syscall.EIO)),
		"joined-dur": errors.Join(context.DeadlineExceeded, publication.ErrDurability),
	} {
		err := q.runtimeWorkerNamed(t.Context(), name, opts, func(context.Context) (bool, error) { return true, cause })
		if err == nil {
			t.Fatalf("%s: swallowed", name)
		}
	}
}

func TestRuntimeClaimsReleased(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	ctx := t.Context()
	if err := q.InitRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.Hint(ctx, req); err != nil {
		t.Fatal(err)
	}
	var id, version int64
	if err := q.db.QueryRowContext(ctx, "SELECT id,version FROM runtime_sources LIMIT 1").Scan(&id, &version); err != nil {
		t.Fatal(err)
	}
	claimed := func() bool {
		var n int
		if err := q.db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM runtime_sources WHERE claim!=0)+(SELECT count(*) FROM runtime_sessions WHERE claim!=0)").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n != 0
	}
	set := func() {
		if _, err := q.db.ExecContext(ctx, "UPDATE runtime_sources SET claim=version; INSERT OR IGNORE INTO runtime_sessions(session) VALUES('s'); UPDATE runtime_sessions SET claim=1"); err != nil {
			t.Fatal(err)
		}
	}
	// Release after the step's own (expired) context.
	set()
	dead, cancel := context.WithCancel(ctx)
	cancel()
	q.releaseSourceClaim(dead, sourceWork{id: id, version: version})
	var sessionID int64
	if err := q.db.QueryRowContext(ctx, "SELECT id FROM runtime_sessions LIMIT 1").Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	q.releaseSessionClaim(dead, sessionID)
	if claimed() {
		t.Fatal("claims survived a failed step")
	}
	// Restart (new Run/InitRuntime) clears anything left behind.
	set()
	r := q.runtimeLocal()
	r.mu.Lock()
	r.initialized = false
	r.mu.Unlock()
	if err := q.InitRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if claimed() {
		t.Fatal("InitRuntime left claims")
	}
}

func TestInterruptedQueryOnExpiredOperationIsTransient(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	op, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	var n int64
	err := q.db.QueryRowContext(op, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x<2000000000) SELECT count(*) FROM c`).Scan(&n)
	if err == nil {
		t.Fatal("query unexpectedly finished")
	}
	t.Logf("observed error type=%T value=%v", err, err)
	expired := errors.Is(op.Err(), context.DeadlineExceeded)
	if !expired || !transientStepError(t.Context(), expired, err) {
		t.Fatalf("not transient: expired=%v %v", expired, err)
	}
	if runtimeFatalCause(err) != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupt leaf must not be fatal in failure paths: %v", err)
	}
	if transientStepError(t.Context(), false, err) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("interrupt without expired op must stay fatal")
	}
}

func TestInitResetsWorkingStates(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	ctx := t.Context()
	if _, err := q.db.ExecContext(ctx, `INSERT INTO enrichment_work(id,session,spec,contract,state) VALUES('e','s',x'7b7d','c','working')`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(ctx, `INSERT INTO optional_repairs(id,session,source_hash,epoch,commitment,source,evidence,identity,state,stage,contract,software) VALUES('r','s','h','e','c',x'7b7d',x'7b7d',x'7b7d','delivering','d','c','v')`); err != nil {
		t.Fatal(err)
	}
	if err := q.initEnrichment(ctx); err != nil {
		t.Fatal(err)
	}
	var e, r string
	if err := q.db.QueryRowContext(ctx, `SELECT (SELECT state FROM enrichment_work),(SELECT state FROM optional_repairs)`).Scan(&e, &r); err != nil || e != "pending" || r != "retry" {
		t.Fatalf("%q %q %v", e, r, err)
	}
}
