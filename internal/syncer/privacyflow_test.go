package syncer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestDirectoryAliasesRetainHistoryUntilDiscoveryIsComplete(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.Claude, "skills", "privacy-fixture", "source.sh")
	testenv.Write(t, path, "echo fixture\n")
	local := store.NewLocal(e.Bucket)
	if first := sweep(t, local); first.Errors != 0 {
		t.Fatalf("initial sweep = %+v", first)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.Claude, "directory-alias")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	second := sweep(t, local)
	if second.Errors != 0 || second.Deleted != 0 || second.SkipReasons["symlink_directory"] == 0 {
		t.Fatalf("directory alias sweep = %+v", second)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), link); err != nil {
		t.Fatal(err)
	}
	third := sweep(t, local)
	if third.Errors != 0 || third.Deleted != 0 || third.SkipReasons["symlink_unresolved"] == 0 {
		t.Fatalf("dangling alias sweep = %+v", third)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	fourth := sweep(t, local)
	if fourth.Errors != 0 || fourth.Deleted != 1 {
		t.Fatalf("complete sweep = %+v", fourth)
	}
}

func TestSkippedSuffixDirectoryAliasesRetainCapturedChildren(t *testing.T) {
	e := testenv.New(t)
	key := "skills/archive.lock/source.ts"
	dir := filepath.Join(e.Claude, "skills", "archive.lock")
	testenv.Write(t, filepath.Join(dir, "source.ts"), "synthetic source\n")
	local := store.NewLocal(e.Bucket)
	if first := sweep(t, local); first.Errors != 0 || len(e.History("claude", key)) != 1 {
		t.Fatalf("initial sweep = %+v", first)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ target, reason string }{
		{t.TempDir(), "symlink_directory"},
		{filepath.Join(t.TempDir(), "missing"), "symlink_unresolved"},
	} {
		if err := os.Symlink(step.target, dir); err != nil {
			t.Fatal(err)
		}
		summary := sweep(t, local)
		if summary.Errors != 0 || summary.Deleted != 0 || summary.SkipReasons[step.reason] == 0 {
			t.Fatalf("alias sweep = %+v, want visible skip and retained history", summary)
		}
		if len(e.History("claude", key)) != 1 {
			t.Fatal("uncertain discovery published a false tombstone")
		}
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
	}
	if last := sweep(t, local); last.Errors != 0 || last.Deleted != 1 {
		t.Fatalf("complete sweep = %+v, want one deletion", last)
	}
	if history := e.History("claude", key); len(history) != 2 || history[1].File.Kind != "delete" {
		t.Fatal("complete discovery did not publish the real deletion")
	}
}
