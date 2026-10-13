package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/gitinfo"
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
	same, err := s.FileUnchanged("/a.jsonl.zst", 20, 5)
	check(t, err)
	gone, err := s.FileUnchanged("/a.jsonl", 20, 5)
	check(t, err)
	if !same || gone {
		t.Errorf("FileUnchanged = %v, %v", same, gone)
	}
}

func TestPiecesAndVersions(t *testing.T) {
	s := open(t)
	check(t, s.SetPieces("k", []raw.Piece{{Start: 0, End: 3, SHA256: "a", Object: "k"}}))
	check(t, s.AddPiece("k", raw.Piece{Start: 3, End: 5, SHA256: "b", Object: "k.d/3-5"}))
	pieces, err := s.Pieces("k")
	check(t, err)
	if len(pieces) != 2 || pieces[1].Object != "k.d/3-5" {
		t.Errorf("Pieces = %v", pieces)
	}
	several := []raw.Piece{{Start: 0, End: 2, SHA256: "x", Object: "k"}, {Start: 2, End: 5, SHA256: "y", Object: "k.d/2-5"}}
	check(t, s.SetPieces("k", several))
	pieces, err = s.Pieces("k")
	check(t, err)
	if diff := cmp.Diff(several, pieces); diff != "" {
		t.Errorf("SetPieces (-want +got):\n%s", diff)
	}
	check(t, s.SetPieces("k", []raw.Piece{{Start: 0, End: 5, SHA256: "c", Object: "k"}}))
	if pieces, _ = s.Pieces("k"); len(pieces) != 1 {
		t.Errorf("fold left %v", pieces)
	}
	if at, err := s.LastVersionAt("k"); at != 0 || err != nil {
		t.Errorf("LastVersionAt before any = %v, %v", at, err)
	}
	v := Version{SHA256: "old", Size: 5, VersionedAt: 10, Pieces: pieces}
	check(t, s.RecordVersion("k", &v))
	check(t, s.RecordVersion("k", &v)) // once per sha256
	versions, err := s.Versions("k")
	check(t, err)
	if diff := cmp.Diff([]Version{v}, versions); diff != "" {
		t.Errorf("Versions:\n%s", diff)
	}
	if at, _ := s.LastVersionAt("k"); at != 10 {
		t.Errorf("LastVersionAt = %v", at)
	}
}

func TestSessionsRootsGitAndPending(t *testing.T) {
	s := open(t)
	if sess, err := s.Session(adapters.SessionRef{Agent: "pi", Key: "s"}); sess != nil || err != nil {
		t.Errorf("Session(missing) = %v, %v", sess, err)
	}
	mem := "m"
	in := &Session{
		Info:      map[string]any{"cwd": "/w"},
		Snapshots: []SnapshotEntry{{ID: "set", MemoryID: &mem, Git: gitinfo.State{Head: "h"}, Timing: "post_hoc", TakenAt: 1, Mtimes: map[string]float64{"x": 2}}},
		Launches:  []map[string]any{{"event": "SessionStart"}},
		FirstSeen: 3,
	}
	check(t, s.PutSession(adapters.SessionRef{Agent: "pi", Key: "s"}, in))
	out, err := s.Session(adapters.SessionRef{Agent: "pi", Key: "s"})
	check(t, err)
	if diff := cmp.Diff(in, out); diff != "" {
		t.Errorf("session round trip:\n%s", diff)
	}
	check(t, s.PutSession(adapters.SessionRef{Agent: "pi", Key: "empty"}, &Session{}))
	if e, _ := s.Session(adapters.SessionRef{Agent: "pi", Key: "empty"}); e == nil || e.Info == nil {
		t.Errorf("empty session = %+v", e)
	}

	check(t, s.AddExtraRoot("pi", "/custom"))
	check(t, s.AddExtraRoot("pi", "/custom"))
	if roots, _ := s.ExtraRoots("pi"); len(roots) != 1 {
		t.Errorf("ExtraRoots = %v", roots)
	}

	check(t, s.MarkManifestPending(adapters.SessionRef{Agent: "pi", Key: "s"}))
	check(t, s.MarkManifestPending(adapters.SessionRef{Agent: "codex", Key: "t"}))
	check(t, s.ManifestWritten(adapters.SessionRef{Agent: "pi", Key: "s"}))
	pending, err := s.PendingManifests()
	check(t, err)
	if diff := cmp.Diff([]adapters.SessionRef{{Agent: "codex", Key: "t"}}, pending); diff != "" {
		t.Errorf("PendingManifests:\n%s", diff)
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

func TestClosedStateReportsErrors(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	check(t, err)
	check(t, s.Close())
	if err := s.RecordUpload("k", "x", 0); err == nil {
		t.Error("write to a closed state succeeded")
	}
	if _, err := s.Pieces("k"); err == nil {
		t.Error("read from a closed state succeeded")
	}
	if _, err := s.Uploaded("k"); err == nil {
		t.Error("read from a closed state succeeded")
	}
}
