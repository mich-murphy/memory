package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

type lstatCounter struct {
	mu           sync.Mutex
	lstat, links map[string]int
}

func newLstatCounter() *lstatCounter {
	return &lstatCounter{lstat: map[string]int{}, links: map[string]int{}}
}

func (c *lstatCounter) Lstat(p string) (os.FileInfo, error) {
	c.mu.Lock()
	c.lstat[p]++
	c.mu.Unlock()
	return os.Lstat(p)
}

func (c *lstatCounter) Readlink(p string) (string, error) {
	c.mu.Lock()
	c.links[p]++
	c.mu.Unlock()
	return os.Readlink(p)
}

func runCall(t *testing.T, r runtimeComposition, config string) capturesource.Result {
	t.Helper()
	res, err := capturesource.Discover(t.Context(), r.callOptions(r.sourceOptions(config)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor != nil {
		if err := res.Cursor.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

// Policy resolve, privacy-owner leaf Lstat and the walk's entry Lstat share one view:
// each path is looked up once per call and again (not from a stale memo) the next call.
func TestDiscoveryCallLstatsEachPathOncePerCall(t *testing.T) {
	c, config, paths := snapshotFixture(t)
	if again, err := filepath.EvalSymlinks(c.Home); err != nil || again != c.Home {
		t.Fatalf("home %q is not symlink-free", c.Home)
	}
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	count := newLstatCounter()
	r.fsBase = count
	for call := 1; call <= 2; call++ {
		if res := runCall(t, r, config); len(res.Requests) != snapshotFiles {
			t.Fatalf("call %d: requests = %d, want %d", call, len(res.Requests), snapshotFiles)
		}
		for p, n := range count.lstat {
			if n != call {
				t.Errorf("call %d: Lstat(%s) = %d total, want %d", call, filepath.Base(p), n, call)
			}
		}
		for p, n := range count.links {
			if n != call {
				t.Errorf("call %d: Readlink(%s) = %d total, want %d", call, filepath.Base(p), n, call)
			}
		}
	}
	if count.lstat[paths[0]] != 2 || count.lstat[filepath.Dir(paths[0])] != 2 {
		t.Errorf("leaf/ancestor Lstat = %d/%d, want 2/2", count.lstat[paths[0]], count.lstat[filepath.Dir(paths[0])])
	}
}

// Memoized discovery returns exactly what the live snapshot policy returns on a tree with
// a state-dir alias, symlinked directory and leaf, and a dangling link.
func TestDiscoveryEquivalenceMemoVersusLiveWithAliases(t *testing.T) {
	c, config, paths := snapshotFixture(t)
	projects := filepath.Join(c.Home, ".claude", "projects")
	for name, target := range map[string]string{"state": c.Spool, "dirlink": filepath.Dir(paths[0]), "leaflink": paths[1], "dangling": filepath.Join(c.Home, "nowhere")} {
		if err := os.Symlink(target, filepath.Join(projects, name)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	memo := discoveryProjection(runCall(t, r, config))
	opts := r.sourceOptions(config)
	live, err := capturesource.Discover(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if live.Cursor != nil {
		_ = live.Cursor.Close()
	}
	if d := cmp.Diff(discoveryProjection(live), memo); d != "" {
		t.Fatalf("memo differs from live (-live +memo):\n%s", d)
	}
	if len(memo) < snapshotFiles+4 {
		t.Fatalf("projection has %d lines, want requests plus alias issues", len(memo))
	}
}

// Capture-time policy stays live: a directory replaced by a link into the state dir
// between two checks on the same composition is refused at the second.
func TestCapturePolicyStaysLiveAcrossChecks(t *testing.T) {
	c, _, paths := snapshotFixture(t)
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	count := newLstatCounter()
	r.fsBase = count
	policy := r.policy(false)
	req := sessioncapture.Request{Path: paths[0]}
	req.Identity.ArchiveID, req.Identity.MachineID, req.Identity.Agent = c.ArchiveID, c.MachineID, "claude"
	if err := policy(t.Context(), req); err != nil {
		t.Fatalf("first check: %v", err)
	}
	dir := filepath.Dir(paths[0])
	if err := os.MkdirAll(c.Spool, 0o700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(c.Spool, "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir); err != nil {
		t.Fatal(err)
	}
	if err := policy(t.Context(), req); err == nil {
		t.Fatal("second check admitted a path whose ancestor now aliases the state dir")
	}
	if len(count.lstat) != 0 {
		t.Errorf("capture-time policy used the discovery memo (%d paths)", len(count.lstat))
	}
}
