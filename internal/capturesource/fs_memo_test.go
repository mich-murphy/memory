package capturesource

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturepolicy"
)

type countingFS struct{ lstat, readlink map[string]int }

func (c *countingFS) Lstat(p string) (os.FileInfo, error) { c.lstat[p]++; return os.Lstat(p) }

func (c *countingFS) Readlink(p string) (string, error) { c.readlink[p]++; return os.Readlink(p) }

func symlinkFreeHome(t *testing.T, o *Options) {
	t.Helper()
	home, err := filepath.EvalSymlinks(o.Home)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := filepath.EvalSymlinks(home); err != nil || again != home {
		t.Fatalf("home %q is not symlink-free", home)
	}
	o.Home = home
}

// One Discover call looks each distinct path up at most once, however many entries
// share its ancestors; the next call (fresh memo) looks them up again.
func TestDiscoverLstatsEachPathOncePerCall(t *testing.T) {
	o := options(t)
	symlinkFreeHome(t, &o)
	root := Root{"claude", filepath.Join(o.Home, ".claude")}
	o.Roots, o.OnlyRoots = []Root{root}, true
	const files = 12
	for i := range files {
		fixture(t, filepath.Join(root.Path, "projects", "slug", "s"+string(rune('a'+i))+".jsonl"), "raw")
	}
	count := &countingFS{map[string]int{}, map[string]int{}}
	for call := 1; call <= 2; call++ {
		oo := o
		oo.Filesystem = capturepolicy.NewFSMemoOver(count)
		if res := discover(t, oo); len(res.Requests) != files {
			t.Fatalf("call %d: requests = %d, want %d", call, len(res.Requests), files)
		}
		for p, n := range count.lstat {
			if n != call {
				t.Errorf("call %d: Lstat(%s) = %d total calls, want %d", call, filepath.Base(p), n, call)
			}
		}
	}
	slug := filepath.Join(root.Path, "projects", "slug")
	if count.lstat[slug] != 2 || count.lstat[filepath.Join(slug, "sa.jsonl")] != 2 {
		t.Errorf("shared ancestor/leaf Lstat = %d/%d, want 2/2 (one per call)", count.lstat[slug], count.lstat[filepath.Join(slug, "sa.jsonl")])
	}
}

// A directory replaced by a symlink between two calls takes effect on the second.
func TestDiscoverSeesDirectoryReplacedBySymlinkOnNextCall(t *testing.T) {
	o := options(t)
	symlinkFreeHome(t, &o)
	root := Root{"claude", filepath.Join(o.Home, ".claude")}
	o.Roots, o.OnlyRoots = []Root{root}, true
	slug := filepath.Join(root.Path, "projects", "slug")
	fixture(t, filepath.Join(slug, "s.jsonl"), "raw")
	if res := discover(t, o); len(res.Requests) != 1 {
		t.Fatalf("first call requests = %d, want 1", len(res.Requests))
	}
	moved := filepath.Join(o.Home, "elsewhere")
	if err := os.Rename(slug, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, slug); err != nil {
		t.Fatal(err)
	}
	res := discover(t, o)
	if len(res.Requests) != 0 || !hasCode(res, "alias_integration_required") {
		t.Fatalf("second call requests = %d, issues = %v, want 0 and alias_integration_required", len(res.Requests), res.Issues)
	}
}
