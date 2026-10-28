package sessioncapture

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestReviewSparseSessionScheduling(t *testing.T) {
	dir, opts, req := setup(t)
	q := open(t, dir, opts)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx, err := q.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for n := range 20000 {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO runtime_sessions(session) VALUES(?)", fmt.Sprintf("inactive-%d", n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	capture(t, q, req)
	start := time.Now()
	for calls := 1; calls <= 400; calls++ {
		id, _, err := q.claimSession(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if id != 0 {
			t.Logf("inactive=20000 due=1 session_claim_calls=%d query_elapsed=%s", calls, time.Since(start))
			return
		}
	}
	t.Fatal("due session not reached")
}

func TestReviewSparseSourceScheduling(t *testing.T) {
	dir, opts, req := setup(t)
	opts.MaxInventoryRecords = 32768
	q := open(t, dir, opts)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx, err := q.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(t.Context(), "INSERT INTO runtime_sources(session,source,request,observed_at,state) VALUES(?,?,?,1,'captured')")
	if err != nil {
		t.Fatal(err)
	}
	for n := range 20000 {
		if _, err := stmt.ExecContext(t.Context(), fmt.Sprintf("inactive-%d", n), "source", []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = stmt.Close() }()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), "INSERT INTO runtime_sources(session,source,request,observed_at,state) VALUES(?,?,?,1,'pending')", req.Identity.BaseKey(), req.SourceID, body); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for calls := 1; calls <= 400; calls++ {
		work, err := q.claimSource(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if work.id != 0 {
			t.Logf("inactive=20000 due=1 claim_calls=%d query_elapsed=%s", calls, time.Since(start))
			return
		}
	}
	t.Fatal("due source not reached")
}
