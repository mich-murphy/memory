package syncer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestDirectoryAndDanglingAliasesFenceOnlyTheirOwnHistory(t *testing.T) {
	for _, tc := range []struct {
		name, key, reason string
		subtree           bool
	}{
		{"directory", "skills/fenced/source.ts", "symlink_directory", true},
		{"danglingSubtree", "skills/fenced/source.ts", "symlink_unresolved", true},
		{"lockedDirectory", "skills/archive.lock/source.ts", "symlink_directory", true},
		{"lockedDangling", "skills/archive.lock/source.ts", "symlink_unresolved", true},
		{"danglingLeaf", "skills/source.ts", "symlink_unresolved", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testenv.New(t)
			path := filepath.Join(e.Claude, filepath.FromSlash(tc.key))
			link := path
			if tc.subtree {
				link = filepath.Dir(path)
			}
			testenv.Write(t, path, "synthetic source\n")
			// These siblings deliberately share the skipped path's textual prefix.
			for _, suffix := range []string{"-warm.ts", "-cold.ts"} {
				testenv.Write(t, link+suffix, "synthetic sibling\n")
			}
			local := store.NewLocal(e.Bucket)
			if first := sweep(t, local); first.Errors != 0 || len(e.History("claude", tc.key)) != 1 {
				t.Fatalf("initial sweep = %+v", first)
			}
			if err := os.RemoveAll(link); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir() // no historical child exists at the new target
			if tc.reason == "symlink_unresolved" {
				target = filepath.Join(target, "missing")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"warm", "cold"} {
				sibling := link + "-" + mode + ".ts"
				if err := os.Remove(sibling); err != nil {
					t.Fatal(err)
				}
				if mode == "cold" {
					if err := os.RemoveAll(e.State); err != nil {
						t.Fatal(err)
					}
				}
				summary := sweep(t, local)
				if summary.Errors != 0 || summary.Deleted != 1 || summary.SkipReasons[tc.reason] == 0 {
					t.Fatalf("%s alias sweep = %+v, want visible skip and one sibling deletion", mode, summary)
				}
				if history := e.History("claude", tc.key); len(history) != 1 || string(e.GenerationContent(history[0].File)) != "synthetic source\n" {
					t.Fatal("fenced source history was changed or tombstoned")
				}
				rel, err := filepath.Rel(e.Claude, sibling)
				if err != nil {
					t.Fatal(err)
				}
				if history := e.History("claude", filepath.ToSlash(rel)); len(history) != 2 || history[1].File.Kind != "delete" {
					t.Fatal("unrelated missing sibling did not receive a tombstone")
				}
			}
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if last := sweep(t, local); last.Errors != 0 || last.Deleted != 1 {
				t.Fatalf("complete sweep = %+v, want one formerly fenced deletion", last)
			}
		})
	}
}

func TestRealDiscoveryFailuresStillBlockRootDeletions(t *testing.T) {
	for _, failure := range []string{"cycle", "unreadable", "export"} {
		t.Run(failure, func(t *testing.T) {
			e := testenv.New(t)
			key := "skills/removed.ts"
			path := filepath.Join(e.Claude, filepath.FromSlash(key))
			testenv.Write(t, path, "synthetic source\n")
			local := store.NewLocal(e.Bucket)
			if first := sweep(t, local); first.Errors != 0 {
				t.Fatalf("initial sweep = %+v", first)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(e.Claude, "skills", "unknown.ts")); err != nil {
				t.Fatal(err)
			}
			broken := filepath.Join(e.Claude, "skills", "broken")
			switch failure {
			case "cycle":
				if err := os.Symlink("broken", broken); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				testenv.Write(t, filepath.Join(broken, "source.ts"), "synthetic source\n")
				if err := os.Chmod(broken, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(broken, 0o700); err != nil {
						t.Error(err)
					}
				})
			case "export":
				broken += ".sqlite"
				testenv.Write(t, broken, "not a database\n")
			}
			if summary := sweep(t, local); summary.Errors == 0 || summary.Deleted != 0 || summary.SkipReasons["symlink_unresolved"] == 0 {
				t.Fatalf("failed-discovery sweep = %+v, want root-wide deletion hold", summary)
			}
			if len(e.History("claude", key)) != 1 {
				t.Fatal("incomplete root published a deletion")
			}
		})
	}
}

func TestExcludedLinkTargetRetainsHistoryWithoutBlockingSiblingDeletion(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.Claude, "skills", "source.ts")
	sibling := filepath.Join(e.Claude, "skills", "sibling.ts")
	testenv.Write(t, path, "synthetic source\n")
	testenv.Write(t, sibling, "synthetic sibling\n")
	local := store.NewLocal(e.Bucket)
	if first := sweep(t, local); first.Errors != 0 {
		t.Fatalf("initial sweep = %+v", first)
	}
	for _, path := range []string{path, sibling} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "auth.json")
	testenv.Write(t, target, "synthetic excluded fixture\n")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if summary := sweep(t, local); summary.Errors != 0 || summary.Deleted != 1 {
		t.Fatalf("excluded-target sweep = %+v, want one unrelated deletion", summary)
	}
	if len(e.History("claude", "skills/source.ts")) != 1 {
		t.Fatal("excluded target caused a false tombstone")
	}
}
