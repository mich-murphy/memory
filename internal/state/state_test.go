package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
)

func open(t *testing.T) *State {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenFailsWhereItCannotWrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	check(t, os.WriteFile(file, nil, 0o600))
	if _, err := Open(t.Context(), file); err == nil {
		t.Error("opened state under a regular file")
	}
	dir := t.TempDir()
	check(t, os.WriteFile(filepath.Join(dir, "state.sqlite3"), []byte("not a database, just text that is long enough"), 0o600))
	if _, err := Open(t.Context(), dir); err == nil {
		t.Error("opened a corrupt database")
	}
}

// The state names every transcript, so Open makes its file private even when something else
// created it.
func TestOpenMakesTheStateFilePrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite3")
	check(t, os.WriteFile(path, nil, 0o600))
	check(t, os.Chmod(path, 0o644))
	s, err := Open(t.Context(), dir)
	check(t, err)
	defer func() { check(t, s.Close()) }()
	st, err := os.Stat(path)
	check(t, err)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("state.sqlite3 mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestWritesSurviveCancellationAndReopen(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	s, err := Open(ctx, dir)
	check(t, err)
	cancel() // a SIGTERM mid-run must not roll back what was already uploaded
	check(t, s.RecordUpload("k", "sha", 3))
	check(t, s.Close())
	s, err = Open(t.Context(), dir)
	check(t, err)
	defer func() { check(t, s.Close()) }()
	u, err := s.Uploaded("k")
	check(t, err)
	if u == nil || u.SHA256 != "sha" || u.Size != 3 {
		t.Errorf("Uploaded = %+v", u)
	}
	if u, err := s.Uploaded("missing"); u != nil || err != nil {
		t.Errorf("Uploaded(missing) = %+v, %v", u, err)
	}
}

func TestFilesKeepHeadsAndPreferTheOneOnDisk(t *testing.T) {
	s := open(t)
	f := File{
		Path: "/a.jsonl", Agent: "codex", Root: "/r", Key: "k", SessionKey: "s", Role: "main", Size: 10, MtimeNS: 5,
		SHA256: "x", LineCount: 2, PrefixSHA256: "p", Head: map[string]any{"cwd": "/w"},
	}
	check(t, s.RecordFile(&f))
	f.Head, f.Size = nil, 20 // a later sync with no head keeps the earlier one
	check(t, s.RecordFile(&f))
	if prev, _ := s.SessionFiles(adapters.SessionRef{Agent: "codex", Key: "s"}); len(prev) != 1 || prev[0].Head["cwd"] != "/w" {
		t.Errorf("head not kept: %+v", prev)
	}
	zst := f
	zst.Path = "/a.jsonl.zst" // Codex recompressed it: same key, new path
	zst.DiskSize = 7
	zst.Head = map[string]any{"cwd": "/z"}
	check(t, s.RecordFile(&zst))
	check(t, s.MarkDeleted("/a.jsonl", 99))
	files, err := s.SessionFiles(adapters.SessionRef{Agent: "codex", Key: "s"})
	check(t, err)
	if len(files) != 1 || files[0].DeletedAt != 0 || files[0].Head["cwd"] != "/z" || files[0].Size != 20 {
		t.Errorf("SessionFiles = %+v", files)
	}
	live, err := s.LiveFiles("codex", "/r")
	check(t, err)
	want := []adapters.SessionFile{{Agent: "codex", Root: "/r", Path: "/a.jsonl.zst", Key: "k", SessionKey: "s", Role: "main"}}
	if diff := cmp.Diff(want, live); diff != "" {
		t.Errorf("LiveFiles:\n%s", diff)
	}
	check(t, s.SetPieces("k", []raw.Piece{{Start: 0, End: 10, SHA256: "a", Object: "k"}, {Start: 10, End: 20, SHA256: "b", Object: "k.d/1"}}))
	same, pieces, err := s.FileUnchanged("/a.jsonl.zst", "k", 7, 5)
	check(t, err)
	gone, _, err := s.FileUnchanged("/a.jsonl", "k", 20, 5)
	check(t, err)
	if !same || gone || pieces != 2 {
		t.Errorf("FileUnchanged = %v, %v, %d pieces", same, gone, pieces)
	}
	if bySize, _, err := s.FileUnchanged("/a.jsonl.zst", "k", 20, 5); err != nil || bySize {
		t.Errorf("FileUnchanged by the uploaded size = %v, %v, want false", bySize, err)
	}
}

// A state written before files had a disk size gains the column, and its files count as
// unchanged by the size it recorded, as they did then, rather than all being read again.
func TestOpenAddsTheDiskSizeToAnOlderState(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.sqlite3"))
	check(t, err)
	_, err = db.ExecContext(t.Context(), `CREATE TABLE files (path TEXT PRIMARY KEY, agent TEXT NOT NULL,
		root TEXT NOT NULL, key TEXT NOT NULL, session_key TEXT, role TEXT NOT NULL, size INTEGER NOT NULL,
		mtime_ns INTEGER NOT NULL, sha256 TEXT NOT NULL, line_count INTEGER, prefix_sha256 TEXT, head TEXT,
		deleted_at REAL);
		INSERT INTO files VALUES ('/a', 'claude', '/r', 'k', NULL, 'aux', 10, 5, 'x', 1, NULL, NULL, NULL)`)
	check(t, err)
	check(t, db.Close())
	for range 2 { // the second Open finds the column there
		s, err := Open(t.Context(), dir)
		check(t, err)
		same, _, err := s.FileUnchanged("/a", "k", 10, 5)
		check(t, err)
		if !same {
			t.Error("FileUnchanged = false for a file recorded before the column")
		}
		check(t, s.RecordFile(&File{Path: "/b", Role: "aux", DiskSize: 3, MtimeNS: 1}))
		check(t, s.Close())
	}
}

func TestRunsKeepTheLast500(t *testing.T) {
	s := open(t)
	before := float64(time.Now().Unix())
	for i := range 505 {
		check(t, s.RecordRun(float64(i), "sweep", map[string]any{"uploaded": i}))
	}
	after := float64(time.Now().Unix() + 1)
	runs, err := s.LastRuns(1000)
	check(t, err)
	if len(runs) != 500 || runs[0].Summary["uploaded"] == nil {
		t.Errorf("%d runs kept, newest %v", len(runs), runs[0])
	}
	if at := runs[0].FinishedAt; at < before || at > after {
		t.Errorf("FinishedAt = %v, want Unix seconds between %v and %v", at, before, after)
	}
}

// A state that can't be used must say so on every read and write, or a sync would upload
// without recording it.
func TestAStateThatCannotBeUsedReportsEveryFailure(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	check(t, err)
	check(t, s.Close())
	if err := s.RecordUpload("k", "x", 0); err == nil {
		t.Error("a write succeeded")
	}
	if _, err := s.Pieces("k"); err == nil {
		t.Error("a list read succeeded")
	}
	if _, err := s.Uploaded("k"); err == nil {
		t.Error("a row read succeeded")
	}
}

// A write that fails part way through recording an upload must not leave the writes before it
// to be committed when the state is closed: the upload would look recorded without its pieces.
func TestAFailedWriteDiscardsTheRecordItWasPartOf(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	check(t, err)
	_, err = s.conn.ExecContext(t.Context(),
		"CREATE TRIGGER fail BEFORE INSERT ON pieces BEGIN SELECT RAISE(ABORT, 'injected'); END")
	check(t, err)
	check(t, s.RecordUpload("k", "sha", 2))
	if err := s.AddPiece("k", raw.Piece{End: 2, SHA256: "sha", Object: "k"}); err == nil {
		t.Fatal("AddPiece succeeded despite the trigger")
	}
	check(t, s.Close())
	s, err = Open(t.Context(), dir)
	check(t, err)
	defer func() { check(t, s.Close()) }()
	if got, err := s.Uploaded("k"); err != nil || got != nil {
		t.Errorf("Uploaded = %v, %v, want nothing", got, err)
	}
}
