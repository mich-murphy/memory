package adapters

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// cleanClosedWALDB returns a WAL-mode database whose last connection closed
// cleanly: header versions are 2/2 and no -wal/-shm/-journal sidecar remains.
func cleanClosedWALDB(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.db")
	producer, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	producer.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE evidence(v INTEGER)", "INSERT INTO evidence VALUES(41),(42),(43)"} {
		if _, err := producer.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile(source)
	if err != nil || len(head) < 20 || head[18] != 2 || head[19] != 2 {
		t.Fatalf("fixture is not WAL-mode: %v", err)
	}
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(source + s); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s remains: %v", s, err)
		}
	}
	return source
}

func TestAuthorizedSQLiteCleanClosedWALBacksUp(t *testing.T) {
	source := cleanClosedWALDB(t)
	dest := filepath.Join(filepath.Dir(source), "prepared.db")
	if err := os.WriteFile(dest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	err = BackupSQLiteAuthorized(t.Context(), source, dest, 1<<30, func() error { return nil },
		SQLiteAuthorizedOptions{Authorize: func(context.Context, SQLiteAuthorization) error { return nil }})
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("source modified", err)
	}
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(source + s); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s created", s)
		}
	}
	out, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	var count, sum int
	if err := out.QueryRowContext(t.Context(), "SELECT count(*),sum(v) FROM evidence").Scan(&count, &sum); err != nil {
		t.Fatal(err)
	}
	if count != 3 || sum != 126 {
		t.Fatalf("rows lost: %d %d", count, sum)
	}
}
