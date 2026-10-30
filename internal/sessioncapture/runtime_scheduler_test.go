package sessioncapture

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Sparse inventories must not spend 100ms worker turns walking retained pages.
func TestSparseDueSelectionSingleClaim(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "session", true: "source"}[source], func(t *testing.T) {
			dir, o, r := setup(t)
			q := open(t, dir, o)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			tx, err := q.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			for n := range 129 {
				if source {
					_, err = tx.ExecContext(t.Context(), `INSERT INTO runtime_sources(session,source,request,observed_at,state) VALUES(?,?,x'7b7d',1,'captured')`, fmt.Sprintf("inactive-%d", n), "source")
				} else {
					_, err = tx.ExecContext(t.Context(), `INSERT INTO runtime_sessions(session) VALUES(?)`, fmt.Sprintf("inactive-%d", n))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if source {
				body, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = q.db.ExecContext(t.Context(), `INSERT INTO runtime_sources(session,source,request,observed_at,state) VALUES(?,?,?,1,'pending')`, r.Identity.BaseKey(), r.SourceID, body); err != nil {
					t.Fatal(err)
				}
				w, err := q.claimSource(t.Context())
				if err != nil || w.id == 0 {
					t.Fatalf("due first claim: %+v %v", w, err)
				}
				w, err = q.claimSource(t.Context())
				if err != nil || w.id != 0 {
					t.Fatalf("busy session: %+v %v", w, err)
				}
			} else {
				capture(t, q, r)
				id, got, err := q.claimSession(t.Context(), false)
				if err != nil || id == 0 || got != r.Identity {
					t.Fatalf("due first claim: %d %+v %v", id, got, err)
				}
				id, _, err = q.claimSession(t.Context(), false)
				if err != nil || id != 0 {
					t.Fatalf("busy session: %d %v", id, err)
				}
			}
		})
	}
}

func TestDueSessionHeadBlocksLaterAndCursorWraps(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := capture(t, q, r)
	write(t, r.Path, "rewrite\n")
	second := capture(t, q, r)
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state='quarantined' WHERE session=? AND sequence=?`, r.Identity.BaseKey(), first.Sequence); err != nil {
		t.Fatal(err)
	}
	id, _, err := q.claimSession(t.Context(), false)
	if err != nil || id != 0 {
		t.Fatalf("quarantined head: %d %v", id, err)
	}
	if _, err = q.db.ExecContext(t.Context(), `UPDATE jobs SET state='retry',next_retry=? WHERE session=? AND sequence=?`, o.Now().UnixNano()+1, r.Identity.BaseKey(), first.Sequence); err != nil {
		t.Fatal(err)
	}
	id, _, err = q.claimSession(t.Context(), false)
	if err != nil || id != 0 {
		t.Fatalf("future head: %d %v", id, err)
	}
	if _, err = q.db.ExecContext(t.Context(), `UPDATE jobs SET next_retry=0 WHERE session=? AND sequence=?`, r.Identity.BaseKey(), first.Sequence); err != nil {
		t.Fatal(err)
	}
	q.runtimeLocal().sessionCursors[0] = 999999
	id, _, err = q.claimSession(t.Context(), false)
	if err != nil || id != 0 {
		t.Fatalf("wrap turn: %d %v", id, err)
	}
	id, got, err := q.claimSession(t.Context(), false)
	if err != nil || id == 0 || got != r.Identity {
		t.Fatalf("wrapped due: %d %+v %v; later=%d", id, got, err, second.Sequence)
	}
}
