package sessioncapture

import (
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// Only sources with the requested ID contribute, and the latest capture wins.
func TestSourceCapturedAtSelectsOnlyMatchingSource(t *testing.T) {
	m := publication.Manifest{Sources: []publication.Source{
		{SourceID: "main", EpochID: "e1", CapturedAtNS: 1_000},
		{SourceID: "subagent-1", EpochID: "e1", CapturedAtNS: 9_000_000},
		{SourceID: "main", EpochID: "e2", CapturedAtNS: 5_000},
		{SourceID: "subagent-2", EpochID: "e1", CapturedAtNS: 7_000_000},
	}}
	for id, want := range map[string]int64{"main": 5_000, "subagent-1": 9_000_000, "subagent-2": 7_000_000, "absent": 0} {
		if got := sourceCapturedAt(m, id); got != want {
			t.Fatalf("%s: captured %d, want %d", id, got, want)
		}
	}
}

// A job whose retry time equals the clock is due; only a strictly later retry
// waits, and a quarantined head is never due.
func TestDueIdentityRetryBoundary(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	capture(t, q, r)
	now := o.Now().UnixNano()
	session := r.Identity.BaseKey()
	for _, tc := range []struct {
		name  string
		retry int64
		state string
		due   bool
	}{
		{"one-ns-ahead", now + 1, "pending", false},
		{"equal-to-now", now, "pending", true},
		{"one-ns-behind", now - 1, "pending", true},
		{"quarantined", now - 1, "quarantined", false},
	} {
		if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET next_retry=?, state=? WHERE session=?`, tc.retry, tc.state, session); err != nil {
			t.Fatal(err)
		}
		id, due, err := q.dueIdentity(t.Context(), session)
		if err != nil || due != tc.due || (due && id != r.Identity) {
			t.Fatalf("%s: identity %+v due %v err %v, want due %v", tc.name, id, due, err, tc.due)
		}
	}
}

// If clearing the scheduling claim fails, the claim must still be released on
// the way out so raw scheduling is not blocked behind a dead worker. The first
// release attempt fails (FAIL keeps the trigger's own counter update); the
// deferred release is the second attempt and must succeed.
func TestDeliveryLaneNextReleasesClaimWhenClearFails(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	capture(t, q, r)
	for _, stmt := range []string{
		`CREATE TABLE test_release_attempts(n INTEGER)`,
		`INSERT INTO test_release_attempts VALUES(0)`,
		`CREATE TRIGGER test_fail_first_release BEFORE UPDATE OF claim ON runtime_sessions WHEN NEW.claim=0 AND (SELECT n FROM test_release_attempts)=0 BEGIN UPDATE test_release_attempts SET n=1; SELECT RAISE(FAIL,'synthetic release failure'); END`,
	} {
		if _, err := q.db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	did, err := q.deliveryLaneNext(t.Context(), newBackend(t), runtimeDefaults(RuntimeOptions{}), false)
	if err == nil || did {
		t.Fatalf("deliveryLaneNext: did=%v err=%v, want the release failure", did, err)
	}
	var claim, attempts int
	if e := q.db.QueryRowContext(t.Context(), `SELECT claim FROM runtime_sessions WHERE session=?`, r.Identity.BaseKey()).Scan(&claim); e != nil {
		t.Fatal(e)
	}
	if e := q.db.QueryRowContext(t.Context(), `SELECT n FROM test_release_attempts`).Scan(&attempts); e != nil {
		t.Fatal(e)
	}
	if attempts != 1 || claim != 0 {
		t.Fatalf("attempts=%d claim=%d, want the failed clear then a successful deferred release", attempts, claim)
	}
}
