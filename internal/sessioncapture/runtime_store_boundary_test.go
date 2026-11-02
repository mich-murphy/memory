package sessioncapture

import "testing"

// A source whose retry time equals the clock is due: only a strictly later retry waits.
func TestClaimSourceRetryEqualToNowIsDue(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	now := o.Now().UnixNano()
	set := func(next int64) {
		if _, err := q.db.ExecContext(t.Context(), `UPDATE runtime_sources SET next_retry=?`, next); err != nil {
			t.Fatal(err)
		}
	}
	set(now + 1)
	if w, err := q.claimSource(t.Context()); err != nil || w.id != 0 {
		t.Fatalf("retry one ns ahead must wait: id=%d err=%v", w.id, err)
	}
	q.runtimeLocal().sourceCursor = 0
	set(now)
	if w, err := q.claimSource(t.Context()); err != nil || w.id == 0 {
		t.Fatalf("retry equal to now must be due: id=%d err=%v", w.id, err)
	}
}
