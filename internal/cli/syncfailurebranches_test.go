package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/testenv"
)

type cancelRecoveryStore struct {
	store.Store
	cancel context.CancelFunc
}

func (s cancelRecoveryStore) OpenReader(ctx context.Context, _ string) (io.ReadCloser, error) {
	s.cancel()
	return nil, ctx.Err()
}

func TestCanceledRecoveryStillRecordsItsRun(t *testing.T) {
	e := testenv.New(t)
	cfg, err := config.Load(config.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bucket := cancelRecoveryStore{Store: store.NewLocal(e.Bucket), cancel: cancel}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	summary, err := syncLocked(ctx, cfg, bucket, &syncFlags{trigger: "canceled"}, "test", log, state.UnixNow())
	if !errors.Is(err, context.Canceled) || summary == nil || summary.Errors != 1 {
		t.Fatalf("syncLocked = %+v, %v, want recorded cancellation", summary, err)
	}
	db, err := state.Open(t.Context(), e.State)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	runs, err := db.LastRuns(10)
	if err != nil || len(runs) != 1 || runs[0].Trigger != "canceled" || runs[0].Summary["errors"] != json.Number("1") {
		t.Fatalf("LastRuns = %v, %v, want one canceled run", runs, err)
	}
}

// Fail only the terminal checkpoint, after recovery and discovery have completed.
type failSealStore struct {
	store.Store
	cause error
}

func (s failSealStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if strings.HasSuffix(key, "/checkpoint.json") && strings.Contains(string(body), `"complete":true`) {
		return s.cause
	}
	return s.Store.Put(ctx, key, body, meta)
}

func TestTerminalSyncFailuresAreRecorded(t *testing.T) {
	for _, phase := range []string{"discovery", "run"} {
		t.Run(phase, func(t *testing.T) {
			e := testenv.New(t)
			cfg, err := config.Load(config.FilePath())
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("synthetic seal failure")
			flags := &syncFlags{trigger: phase, agents: agentList{"pi"}}
			var bucket store.Store = store.NewLocal(e.Bucket)
			want := "archive root: unsupported path identity"
			if phase == "discovery" {
				// A hook-inferred root must reject invalid UTF-8 before publication.
				invalidName := "invalid-" + string([]byte{0xff})
				flags.paths = []string{filepath.Join(e.Tmp, invalidName, testenv.PiKey+".jsonl")}
			} else {
				// No source files: reach Run's terminal checkpoint without uploads.
				flags.paths = []string{filepath.Join(e.Tmp, "unclassified.txt")}
				bucket = failSealStore{Store: bucket, cause: cause}
				want = cause.Error()
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			summary, err := syncLocked(t.Context(), cfg, bucket, flags, "test", log, state.UnixNow())
			if err == nil || !strings.Contains(err.Error(), want) || summary == nil || summary.Errors != 1 {
				t.Fatalf("syncLocked = %+v, %v, want one %s error", summary, err, phase)
			}
			if phase == "run" && !errors.Is(err, cause) {
				t.Fatalf("terminal failure lost its cause: %v", err)
			}
			db, err := state.Open(t.Context(), e.State)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			runs, err := db.LastRuns(10)
			if err != nil || len(runs) != 1 || runs[0].Trigger != phase || runs[0].Summary["errors"] != json.Number("1") {
				t.Fatalf("LastRuns = %v, %v, want one failed run", runs, err)
			}
		})
	}
}

func TestRecordRunFailurePreservesPrimaryError(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "record-only", true: "primary-and-record"}[failed], func(t *testing.T) {
			e := testenv.New(t)
			cfg, err := config.Load(config.FilePath())
			if err != nil {
				t.Fatal(err)
			}
			db, err := state.Open(t.Context(), e.State)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			// Fail the actual RecordRun INSERT, without changing production APIs.
			sqlDB, err := sql.Open("sqlite", filepath.Join(e.State, "state.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := sqlDB.Close(); err != nil {
					t.Error(err)
				}
			}()
			if _, err := sqlDB.ExecContext(t.Context(), `CREATE TRIGGER fail_run BEFORE INSERT ON runs BEGIN SELECT RAISE(FAIL, 'synthetic record failure'); END`); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("synthetic seal failure")
			var bucket store.Store = store.NewLocal(e.Bucket)
			wantErrors := 0
			if failed {
				bucket = failSealStore{Store: bucket, cause: cause}
				wantErrors = 1
			}
			flags := &syncFlags{agents: agentList{"pi"}, paths: []string{filepath.Join(e.Tmp, "unclassified.txt")}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			summary, err := syncLocked(t.Context(), cfg, bucket, flags, "test", log, state.UnixNow())
			if err == nil || !strings.Contains(err.Error(), "synthetic record failure") || errors.Is(err, cause) != failed {
				t.Fatalf("syncLocked error = %v, want record failure and primary=%v", err, failed)
			}
			if summary == nil || summary.Errors != wantErrors {
				t.Fatalf("summary = %+v, want errors=%d", summary, wantErrors)
			}
			var count int
			if err := sqlDB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM runs").Scan(&count); err != nil || count != 0 {
				t.Fatalf("runs = %d, %v, want no successful record", count, err)
			}
		})
	}
}
