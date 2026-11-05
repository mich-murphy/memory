package sessioncapture

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// syncRecorder counts inbox tree fences and every directory fsync. It installs
// package-level test hooks, so users must not run in parallel; Cleanup restores them.
type syncRecorder struct {
	mu   sync.Mutex
	tree []string
	dirs []string
}

func recordSyncs(t *testing.T) *syncRecorder {
	t.Helper()
	r := &syncRecorder{}
	tree := func(p string) {
		r.mu.Lock()
		r.tree = append(r.tree, p)
		r.mu.Unlock()
	}
	dir := func(p string) {
		r.mu.Lock()
		r.dirs = append(r.dirs, p)
		r.mu.Unlock()
	}
	inboxTreeSyncHook.Store(&tree)
	syncDirHook.Store(&dir)
	t.Cleanup(func() {
		inboxTreeSyncHook.Store(nil)
		syncDirHook.Store(nil)
	})
	return r
}

func (r *syncRecorder) trees() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.tree)
}

func (r *syncRecorder) dirCount(p string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, d := range r.dirs {
		if d == p {
			n++
		}
	}
	return n
}

// fenceBase returns a symlink-free temp dir (macOS /var is /private/var).
func fenceBase(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if again, err := filepath.EvalSymlinks(base); err != nil || again != base {
		t.Fatalf("base %q is not symlink-free (%q, %v)", base, again, err)
	}
	return base
}

// existingInbox creates the spool, the named inbox and its lock file without going
// through lockNamedInbox, as a previous process would have left them.
func existingInbox(t *testing.T, spool, name string) (dir, lockPath string) {
	t.Helper()
	dir, lockPath = filepath.Join(spool, name), filepath.Join(spool, name+".lock")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, lockPath
}

func lockTick(t *testing.T, spool, name string) {
	t.Helper()
	_, lock, err := lockNamedInbox(t.Context(), spool, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestInboxTreeFenceOnlyOnFirstLockOfExistingInbox(t *testing.T) {
	for _, name := range []string{"hints", "native-evidence"} {
		t.Run(name, func(t *testing.T) {
			spool := filepath.Join(fenceBase(t), "spool")
			dir, _ := existingInbox(t, spool, name)
			rec := recordSyncs(t)
			for range 6 {
				lockTick(t, spool, name)
			}
			if got := rec.trees(); !slices.Equal(got, []string{dir}) {
				t.Fatalf("tree syncs %v, want exactly one for %s", got, dir)
			}
		})
	}
}

func TestDrainTicksFenceInboxTreeOnce(t *testing.T) {
	_, o, _ := setup(t)
	qdir := filepath.Join(fenceBase(t), "queue")
	q := open(t, qdir, o)
	hints, _ := existingInbox(t, q.dir, "hints")
	native, _ := existingInbox(t, q.dir, "native-evidence")
	rec := recordSyncs(t)
	for range 5 {
		if err := q.drainHints(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := q.drainNativeEvidence(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	got := rec.trees()
	slices.Sort(got)
	want := []string{hints, native}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("tree syncs %v, want one per inbox %v", got, want)
	}
}

func TestInboxFreshSpoolFirstLockSyncsWholeTree(t *testing.T) {
	base := fenceBase(t)
	spool := filepath.Join(base, "spool")
	dir := filepath.Join(spool, "hints")
	rec := recordSyncs(t)
	lockTick(t, spool, "hints")
	lockTick(t, spool, "hints")
	if got := rec.trees(); !slices.Equal(got, []string{dir}) {
		t.Fatalf("tree syncs %v, want exactly [%s]", got, dir)
	}
	for _, d := range []string{dir, spool, base} {
		if n := rec.dirCount(d); n != 1 {
			t.Fatalf("%s fsynced %d times, want 1", d, n)
		}
	}
}

func TestInboxFenceRepeatsWhenIdentityOrCreationChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, dir, lock string)
	}{
		{"inbox-removed-and-recreated", func(t *testing.T, dir, _ string) {
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
		}},
		{"inbox-replaced-keeping-old-inode", func(t *testing.T, dir, _ string) {
			if err := os.Rename(dir, dir+".old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"lock-replaced-keeping-old-inode", func(t *testing.T, _, lock string) {
			if err := os.Rename(lock, lock+".old"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(lock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"lock-removed-and-recreated", func(t *testing.T, _, lock string) {
			if err := os.Remove(lock); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spool := filepath.Join(fenceBase(t), "spool")
			dir, lock := existingInbox(t, spool, "hints")
			rec := recordSyncs(t)
			lockTick(t, spool, "hints")
			lockTick(t, spool, "hints")
			if got := rec.trees(); !slices.Equal(got, []string{dir}) {
				t.Fatalf("before change: tree syncs %v, want one", got)
			}
			tc.mutate(t, dir, lock)
			lockTick(t, spool, "hints")
			if got := rec.trees(); !slices.Equal(got, []string{dir, dir}) {
				t.Fatalf("after change: tree syncs %v, want a second", got)
			}
			lockTick(t, spool, "hints")
			if n := len(rec.trees()); n != 2 {
				t.Fatalf("tree syncs %d after settling, want 2", n)
			}
		})
	}
}

// created forces a fence even when the recorded identity matches, which a reused
// inode after remove+recreate would otherwise hide.
func TestFenceInboxCreatedForcesSyncDespiteMatchingIdentity(t *testing.T) {
	spool := filepath.Join(fenceBase(t), "spool")
	dir, lock := existingInbox(t, spool, "hints")
	rec := recordSyncs(t)
	for i, c := range []bool{false, false, true, false} {
		if err := fenceInbox(dir, lock, c); err != nil {
			t.Fatal(err)
		}
		want := []int{1, 1, 2, 2}[i]
		if n := len(rec.trees()); n != want {
			t.Fatalf("step %d (created=%v): %d tree syncs, want %d", i, c, n, want)
		}
	}
}

// Draining an adopted native record still fsyncs the inbox after removing the file,
// and does so without re-fencing the already fenced tree.
func TestNativeDrainKeepsRemovalDirSync(t *testing.T) {
	_, o, _ := setup(t)
	base := fenceBase(t)
	qdir := filepath.Join(base, "queue")
	q := open(t, qdir, o)
	identity := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "claude", SessionID: "session"}
	record := NativeEvidence{Contract: EnrichmentContract, ID: "launch-a", Identity: identity, Request: Request{Identity: identity, SourceID: "source", Role: "main", Path: filepath.Join(base, "absent")}, ObservedAtNS: 1, Context: NativeContext{Event: "start", ContextOrigin: "native"}}
	if err := WriteNativeEvidence(t.Context(), qdir, record); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(qdir, "native-evidence")
	rec := recordSyncs(t)
	if err := q.drainNativeEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("adopted record not removed: %d entries, %v", len(entries), err)
	}
	if got := rec.trees(); len(got) != 0 {
		t.Fatalf("already fenced inbox re-fenced: %v", got)
	}
	if n := rec.dirCount(dir); n != 1 {
		t.Fatalf("native inbox fsynced %d times after removal, want 1", n)
	}
}
