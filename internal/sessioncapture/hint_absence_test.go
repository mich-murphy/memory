package sessioncapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

type absentFixture struct {
	dir     string
	q       *Queue
	absent  Request
	healthy Request
	roots   [2]string
}

// newAbsentFixture builds a classified Claude main request whose source was
// deleted after its hint was produced, plus a healthy one under another root.
func newAbsentFixture(t *testing.T) absentFixture {
	t.Helper()
	dir, o, _ := setup(t)
	f := absentFixture{dir: dir, q: open(t, dir, o)}
	f.roots = [2]string{filepath.Join(t.TempDir(), "gone"), filepath.Join(t.TempDir(), "live")}
	f.absent = ownerRequest(t, f.roots[0], "one", false)
	f.healthy = ownerRequest(t, f.roots[1], "two", false)
	if err := os.Remove(f.absent.Path); err != nil {
		t.Fatal(err)
	}
	return f
}

// sourceStates maps "root-index" to runtime state, and reports ownership presence.
func (f absentFixture) states(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.q.db.QueryContext(t.Context(), `SELECT session,state FROM runtime_sources`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var session, state string
		if err := rows.Scan(&session, &state); err != nil {
			t.Fatal(err)
		}
		got[session] = state
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func (f absentFixture) owned(t *testing.T, root string) bool {
	t.Helper()
	_, _, err := f.q.LookupOwnership(t.Context(), "claude", root, testOwnerUUID)
	if err != nil && !errors.Is(err, ErrOwnershipPending) {
		t.Fatal(err)
	}
	return err == nil
}

func (f absentFixture) wantAdmitted(t *testing.T, state string) {
	t.Helper()
	want := map[string]string{f.absent.Identity.BaseKey(): state, f.healthy.Identity.BaseKey(): state}
	if diff := cmp.Diff(want, f.states(t)); diff != "" {
		t.Fatalf("runtime_sources (-want +got):\n%s", diff)
	}
	if f.owned(t, f.roots[0]) || !f.owned(t, f.roots[1]) {
		t.Fatalf("ownership: absent=%v healthy=%v, want false,true", f.owned(t, f.roots[0]), f.owned(t, f.roots[1]))
	}
}

func TestDrainHintsToleratesAbsentClassifiedSource(t *testing.T) {
	f := newAbsentFixture(t)
	for _, r := range []Request{f.absent, f.healthy} {
		if err := WriteHint(t.Context(), f.dir, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(f.dir, "hints"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("hints left: %d %v", len(entries), err)
	}
	f.wantAdmitted(t, "pending")
	// Later capture classifies the admitted absent source, without retry.
	for range 2 {
		if _, err := f.q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{f.absent.Identity.BaseKey(): "absent", f.healthy.Identity.BaseKey(): "captured"}
	if diff := cmp.Diff(want, f.states(t)); diff != "" {
		t.Fatalf("after capture (-want +got):\n%s", diff)
	}
}

func TestInboxLoopSurvivesAbsentClassifiedSource(t *testing.T) {
	f := newAbsentFixture(t)
	for _, r := range []Request{f.absent, f.healthy} {
		if err := WriteHint(t.Context(), f.dir, r); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.q.inboxLoop(ctx) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("inbox loop stopped: %v", err)
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("hints not drained")
		case <-ticker.C:
			entries, err := os.ReadDir(filepath.Join(f.dir, "hints"))
			if err != nil || len(entries) != 0 {
				continue
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("inbox loop: %v", err)
			}
			f.wantAdmitted(t, "pending")
			return
		}
	}
}

func TestHintBatchToleratesAbsentClassifiedSource(t *testing.T) {
	f := newAbsentFixture(t)
	if err := f.q.HintBatch(t.Context(), []Request{f.absent, f.healthy}); err != nil {
		t.Fatal(err)
	}
	f.wantAdmitted(t, "pending")
}

// Ownership enrichment stays fatal for non-absence failures: a policy-storage
// failure on an existing source must still surface from Hint.
func TestHintOwnershipPolicyStorageFailureRemainsFatal(t *testing.T) {
	f := newAbsentFixture(t)
	missing := filepath.Join(filepath.Dir(f.dir), "missing-policy")
	f.q.opts.CheckPolicy = func(context.Context, Request) error { _, err := os.Stat(missing); return err }
	err := f.q.Hint(t.Context(), f.healthy)
	if err == nil || runtimeFatalCause(err) == nil || onlySourceAbsence(err) {
		t.Fatalf("policy storage failure not fatal: %v", err)
	}
}
