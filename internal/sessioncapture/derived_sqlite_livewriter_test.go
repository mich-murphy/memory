package sessioncapture

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// A live writer that commits during every capture attempt must never quarantine the
// source: the changed-source failure is transient and retries without spending the
// budget. Once the writer pauses the next attempt captures, and the delivered bytes
// restore to a consistent database holding every committed row.
func TestDerivedSQLiteLiveWriterRetriesThenCaptures(t *testing.T) {
	dir, o, r := setup(t)
	r.Path = filepath.Join(filepath.Dir(r.Path), "session.sqlite")
	r.Transform = "sqlite-backup"
	db := sqliteQueueDB(t, r.Path, true, "seed")
	writing := true
	o.CheckPolicy = func(ctx context.Context, _ Request) error {
		if writing {
			if _, err := db.ExecContext(ctx, "INSERT INTO evidence VALUES ('live')"); err != nil {
				t.Error(err)
			}
		}
		return nil
	}
	clock := time.Unix(1000, 0)
	o.Now = func() time.Time { return clock }
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{MaxAttempts: 2})
	for i := 1; i <= 6; i++ {
		captureTurns(t, q, opts)
		clock = clock.Add(10 * time.Minute)
		src, err := q.WorkStatus(t.Context(), 0, 8)
		if err != nil || len(src) != 1 || src[0].State != "retry" || src[0].Attempts < i {
			t.Fatalf("attempt %d: %+v %v", i, src, err)
		}
		if entries, err := q.Quarantine(t.Context(), 0, 8); err != nil || len(entries) != 0 {
			t.Fatalf("attempt %d quarantined: %+v %v", i, entries, err)
		}
	}
	writing = false
	captureTurns(t, q, opts)
	if src, err := q.WorkStatus(t.Context(), 0, 8); err != nil || src[0].State != "captured" {
		t.Fatalf("paused writer not captured: %+v %v", src, err)
	}
	var committed int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM evidence").Scan(&committed); err != nil || committed < 9 {
		t.Fatalf("writer rows: %d %v", committed, err)
	}
	var verdict string
	if err := db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&verdict); err != nil || verdict != "ok" {
		t.Fatalf("writer database damaged: %q %v", verdict, err)
	}
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "export")
	exported, err := publication.ExportSession(t.Context(), backend, r.Identity, dest)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", "file:"+filepath.Join(dest, exported.Bodies[0].Path)+"?immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	if err := restored.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&verdict); err != nil || verdict != "ok" {
		t.Fatalf("restored database damaged: %q %v", verdict, err)
	}
	var live, seeds int
	if err := restored.QueryRowContext(t.Context(), "SELECT count(*) FILTER (WHERE body='live'), count(*) FILTER (WHERE body='seed') FROM evidence").Scan(&live, &seeds); err != nil {
		t.Fatal(err)
	}
	if seeds != 3 || live != committed-3 {
		t.Fatalf("restored %d seed + %d live rows, writer committed %d", seeds, live, committed)
	}
}
