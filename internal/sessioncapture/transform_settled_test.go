package sessioncapture

import "testing"

// A transformed source with an unchanged discovery fingerprint is a write-free no-op;
// a changed input (for example the SQLite -wal) re-hints exactly once.
func TestTransformedSourceSettledSweepsAreWriteFree(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	r.Transform = "sqlite-backup"
	r.Fingerprint = "wal-0"
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	before := rowOf(t, q, r)
	for i := range 3 {
		r.ObservedAtNS = int64(500 + i)
		if err := q.HintBatch(t.Context(), []Request{r}); err != nil {
			t.Fatal(err)
		}
	}
	if after := rowOf(t, q, r); after != before {
		t.Fatalf("settled sweeps changed row: %+v != %+v", after, before)
	}
	pending := func() (n int) {
		if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_sources WHERE state='pending'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := pending(); n != 1 {
		t.Fatalf("pending before settling=%d, want 1", n)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE runtime_sources SET state='captured'`); err != nil {
		t.Fatal(err)
	}
	before = rowOf(t, q, r)
	for i := range 3 {
		r.ObservedAtNS = int64(900 + i)
		if err := q.HintBatch(t.Context(), []Request{r}); err != nil {
			t.Fatal(err)
		}
	}
	if after := rowOf(t, q, r); after != before || pending() != 0 {
		t.Fatalf("settled sweeps must not re-hint: %+v from %+v pending=%d", after, before, pending())
	}
	r.Fingerprint = "wal-1"
	if err := q.HintBatch(t.Context(), []Request{r}); err != nil {
		t.Fatal(err)
	}
	if s := rowOf(t, q, r); s.version != before.version+1 || s.state != "pending" {
		t.Fatalf("wal change must re-hint once: %+v from %+v", s, before)
	}
	if n := pending(); n != 1 {
		t.Fatalf("pending=%d, want 1", n)
	}
}
