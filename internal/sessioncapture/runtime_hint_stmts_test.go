package sessioncapture

import "testing"

// A statement is prepared once per transaction and reused; a statement that cannot
// be prepared falls back to the plain call, which reports the engine's error.
func TestTxStmtsCachesAndFallsBack(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	tx, err := q.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	c := newTxStmts(tx)
	defer c.close()
	for range 3 {
		var n int
		if err := c.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM jobs WHERE session=?`, "x").Scan(&n); err != nil || n != 0 {
			t.Fatalf("count %d, %v", n, err)
		}
		if _, err := c.ExecContext(t.Context(), `DELETE FROM jobs WHERE session=?`, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.stmts) != 2 {
		t.Fatalf("%d cached statements, want 2", len(c.stmts))
	}
	if _, err := c.ExecContext(t.Context(), `NOT SQL`); err == nil {
		t.Fatal("unpreparable exec must fail")
	}
	var n int
	if err := c.QueryRowContext(t.Context(), `NOT SQL`).Scan(&n); err == nil {
		t.Fatal("unpreparable query must fail")
	}
	if len(c.stmts) != 2 {
		t.Fatalf("failed prepare was cached: %d statements", len(c.stmts))
	}
}
