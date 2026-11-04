package capturesource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const freshFiles = 3

// freshFixture has a real project dir; the state dir is a path that does not yet exist.
// Creating state as a symlink to the project dir makes every project file excluded by
// filesystem identity (SameFile ancestor walk), which only a fresh stat can observe.
func freshFixture(t *testing.T) (opts Options, project, state string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	root := filepath.Join(home, ".claude")
	project = filepath.Join(root, "projects", "q")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range freshFiles {
		name := fmt.Sprintf("%08x-0000-4000-8000-%012x.jsonl", i+1, i+1)
		if err := os.WriteFile(filepath.Join(project, name), []byte("synthetic\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state = filepath.Join(dir, "state")
	opts = Options{
		ArchiveID: "a", MachineID: "m", Home: home, Roots: []Root{{Agent: "claude", Path: root}}, OnlyRoots: true,
		ConfigPath: filepath.Join(dir, "cfg", "config.json"), StateDir: state, EntriesPerRoot: 64,
		Policy: func(context.Context, Scope) error { return nil },
	}
	return opts, project, state
}

func discoverCount(t *testing.T, opts Options) int {
	t.Helper()
	res, err := Discover(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor != nil {
		if err := res.Cursor.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return len(res.Requests)
}

// A state alias that appears between two Discover calls must exclude on the next call.
func TestDiscoverFreshnessStateAliasAppearsBetweenCalls(t *testing.T) {
	opts, project, state := freshFixture(t)
	if got := discoverCount(t, opts); got != freshFiles {
		t.Fatalf("first call requests = %d, want %d", got, freshFiles)
	}
	if err := os.Symlink(project, state); err != nil {
		t.Fatal(err)
	}
	if got := discoverCount(t, opts); got != 0 {
		t.Fatalf("second call requests = %d, want 0 (state dir now aliases the project)", got)
	}
}

// Within one call the stat snapshot holds: a change after the first evaluation is not
// observed until the next call. This pins the per-call memo (and its absence across calls).
func TestDiscoverSnapshotHoldsWithinCall(t *testing.T) {
	opts, project, state := freshFixture(t)
	calls := 0
	opts.Policy = func(context.Context, Scope) error {
		calls++
		if calls == 3 { // after the root's first evaluation stat'ed the missing state dir
			if err := os.Symlink(project, state); err != nil {
				t.Error(err)
			}
		}
		return nil
	}
	if got := discoverCount(t, opts); got != freshFiles {
		t.Fatalf("in-call requests = %d, want %d (snapshot must not see the mid-call change)", got, freshFiles)
	}
	opts.Policy = func(context.Context, Scope) error { return nil }
	if got := discoverCount(t, opts); got != 0 {
		t.Fatalf("next call requests = %d, want 0", got)
	}
}
