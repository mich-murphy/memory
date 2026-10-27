package sessioncapture

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A concurrent read-only inspector (work-status) may hold a SHARED lock while
// the coordinator opens a fresh or recovering queue. Open must wait for it
// within the busy timeout instead of failing immediately with SQLITE_BUSY.
func TestOpenWaitsForConcurrentReaderLock(t *testing.T) {
	dir, o, _ := setup(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "queue.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}.Encode()}
	rdb, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rdb.Close() }()
	rdb.SetMaxOpenConns(1)
	tx, err := rdb.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(500 * time.Millisecond); _ = tx.Rollback() }()
	start := time.Now()
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatalf("Open after %v: %v", time.Since(start), err)
	}
	_ = q.Close()
}

// A second writer against a held flock reports the typed single-writer conflict.
func TestOpenReportsQueueOpenSentinel(t *testing.T) {
	dir, o, _ := setup(t)
	open(t, dir, o) // Cleanup closes the first writer.
	if second, err := Open(t.Context(), dir, o); !errors.Is(err, ErrQueueOpen) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second Open: %v", err)
	}
}
