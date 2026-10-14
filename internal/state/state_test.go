package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	same, pieces, err := s.FileUnchanged("/a.jsonl.zst", "k", 20, 5)
	check(t, err)
	gone, _, err := s.FileUnchanged("/a.jsonl", "k", 20, 5)
	check(t, err)
	if !same || gone || pieces != 2 {
		t.Errorf("FileUnchanged = %v, %v, %d pieces", same, gone, pieces)
	}
}

func TestRunsKeepTheLast500(t *testing.T) {
	s := open(t)
	for i := range 505 {
		check(t, s.RecordRun(float64(i), "sweep", map[string]any{"uploaded": i}))
	}
	runs, err := s.LastRuns(1000)
	check(t, err)
	if len(runs) != 500 || runs[0].Summary["uploaded"] == nil {
		t.Errorf("%d runs kept, newest %v", len(runs), runs[0])
	}
}
