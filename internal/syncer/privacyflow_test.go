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
