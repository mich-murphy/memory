package capturepolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoizedStatsEachPathOnceAndKeepsDecisions(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(state, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	p, err := New("", state)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	m := p.memoizedOver(func(s string) (os.FileInfo, error) { counts[s]++; return os.Stat(s) })
	cases := map[string]bool{
		filepath.Join(dir, "link", "f"):  true,
		filepath.Join(dir, "state", "f"): true,
		filepath.Join(dir, "other", "f"): false,
	}
	for range 3 {
		for path, want := range cases {
			if got := m.Excludes([]string{path}); got != want || p.Excludes([]string{path}) != want {
				t.Fatalf("Excludes(%s) memo=%t plain=%t, want %t", filepath.Base(filepath.Dir(path)), got, p.Excludes([]string{path}), want)
			}
		}
	}
	for path, n := range counts {
		if n != 1 {
			t.Errorf("stat(%s) called %d times, want 1", path, n)
		}
	}
}

func TestMemoizedSnapshotIsStaleButFreshOneSeesChange(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	p, err := New("", state)
	if err != nil {
		t.Fatal(err)
	}
	id := []string{filepath.Join(dir, "link", "f")}
	old := p.Memoized()
	if old.Excludes(id) {
		t.Fatal("alias excluded before it exists")
	}
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(state, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if old.Excludes(id) {
		t.Error("snapshot unexpectedly observed a later change")
	}
	if !p.Memoized().Excludes(id) || !p.Excludes(id) {
		t.Error("fresh snapshot missed the state alias created after the previous one")
	}
}
