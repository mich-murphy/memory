package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// waitrun TRIGGER waits for a durable run log entry, not just publication or removal
// of the pending marker (which happens before sync starts). Like waitfor, its bound
// is 20 seconds. This is an observer: it must not create/migrate state or take the
// sync lock, and the script still checks the run's outcome with doctor.
func waitRun(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: waitrun trigger")
	}
	path := filepath.Join(ts.Getenv("AGENT_SESSIONS_STATE_DIR"), "state.sqlite3")
	ts.Check(waitForRun(context.Background(), path, args[0], 20*time.Second))
}

func waitForRun(ctx context.Context, path, trigger string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		found, err := runRecorded(ctx, path, trigger)
		if err == nil && found {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("waited %v for completed run %q: %w", timeout, trigger, errors.Join(ctx.Err(), lastErr))
		case <-ticker.C:
		}
	}
}

func runRecorded(ctx context.Context, path, trigger string) (bool, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(50)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }() // observer has no writes to flush
	db.SetMaxOpenConns(1)
	var found bool
	err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE trigger = ?)", trigger).Scan(&found)
	return found, err
}

func completionDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	// URI-sensitive characters also exercise safe DSN construction in the observer.
	path := filepath.Join(t.TempDir(), "state ?#.sqlite3")
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE runs (trigger TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestWaitForRunCommittedCompletion(t *testing.T) {
	path, db := completionDB(t)
	if _, err := db.ExecContext(t.Context(), "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() // already committed on success
	if _, err := tx.ExecContext(t.Context(), "INSERT INTO runs VALUES (?)", "sweep:session_shutdown"); err != nil {
		t.Fatal(err)
	}
	// Deterministic completion barrier: objects/marker state could already be
	// visible, but even an inserted run is not completion until its commit.
	found, err := runRecorded(t.Context(), path, "sweep:session_shutdown")
	if err != nil || found {
		t.Fatalf("uncommitted run visible: found=%v err=%v", found, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := waitForRun(t.Context(), path, "sweep:session_shutdown", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForRunWrongTrigger(t *testing.T) {
	path, db := completionDB(t)
	if _, err := db.ExecContext(t.Context(), "INSERT INTO runs VALUES (?)", "sweep:session_start"); err != nil {
		t.Fatal(err)
	}
	// SQL metacharacters must remain a literal trigger, not alter the predicate.
	for _, trigger := range []string{"sweep:session_shutdown", "' OR 1=1 --"} {
		err := waitForRun(t.Context(), path, trigger, 20*time.Millisecond)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("trigger %q: expected deadline, got %v", trigger, err)
		}
	}
}

func TestWaitForRunMissingStateDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sqlite3")
	err := waitForRun(t.Context(), path, "sweep:session_shutdown", 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("observer created missing state: %v", err)
	}
}

func TestWaitForRunDoesNotMigrate(t *testing.T) {
	path, db := completionDB(t)
	if _, err := db.ExecContext(t.Context(), "DROP TABLE runs"); err != nil {
		t.Fatal(err)
	}
	err := waitForRun(t.Context(), path, "sweep:session_shutdown", 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("expected missing schema diagnostic, got %v", err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("observer created %d tables", count)
	}
}

func TestWaitForRunLockedStateIsBounded(t *testing.T) {
	path, db := completionDB(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(t.Context(), "ROLLBACK") }()
	started := time.Now()
	err = waitForRun(t.Context(), path, "sweep:session_shutdown", 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline under exclusive lock, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("observer blocked for %v under exclusive lock", elapsed)
	}
}
