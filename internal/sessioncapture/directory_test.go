package sessioncapture

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestQueueNestedDirectoriesAndDurabilitySettings(t *testing.T) {
	dir, opts, req := setup(t)
	dir = filepath.Join(dir, "new-parent", "new-child", "queue")
	q := open(t, dir, opts)
	var mode string
	var synchronous int
	if err := q.db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" || synchronous != 2 {
		t.Fatalf("authoritative queue durability settings: %q %d", mode, synchronous)
	}
	bad := req
	bad.SourceID = string([]byte{0xff})
	if _, err := q.Capture(t.Context(), bad); err == nil {
		t.Fatal("lossy source identity must fail before acceptance")
	}
	status, err := q.Status(t.Context())
	if err != nil || len(status) != 0 {
		t.Fatalf("invalid wire identity advanced durable progress: %v", err)
	}
	capture(t, q, req)
	if err := syncQueueTree(dir); err != nil {
		t.Fatalf("existing tree must remain fenceable on retry: %v", err)
	}
	if err := syncQueueTree(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing required directory must not be acknowledged: %v", err)
	}
}
