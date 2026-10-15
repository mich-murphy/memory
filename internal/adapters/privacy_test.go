package adapters

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestDiscoveryCapturesRegularSymlinksAndReportsDirectoryUncertainty(t *testing.T) {
	a := NewPi(t.TempDir())
	root := a.CanonicalRoot()
	outside := t.TempDir()
	writeSource(t, filepath.Join(outside, "source.ts"), "source bytes\n")
	writeSource(t, filepath.Join(root, "npm", "pkg", "package-lock.json"), "{}")
	writeSource(t, filepath.Join(root, "git", "pkg", "yarn.lock"), "exact-version")
	writeSource(t, filepath.Join(root, "git", "pkg", "source.ts"), "source")
	for _, name := range []string{"migration-backups/old.json", "Foo.app/source", ".DS_Store", "one_cache.json", "two-cache.json", "logs_1.sqlite", ".codex-global-state.json.before-upgrade"} {
		writeSource(t, filepath.Join(root, name), "noise")
	}
	link := filepath.Join(root, "extensions", "alias.ts")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "source.ts"), link); err != nil {
		t.Fatal(err)
	}
	got := Discover(a, root)
	if !got.Complete || len(got.Files) != 4 || len(got.Databases) != 0 {
		t.Fatalf("Discover = %+v", got)
	}
	if !slices.ContainsFunc(got.Files, func(sf SessionFile) bool { return sf.Path == link && sf.Key == "extensions/alias.ts" }) {
		t.Fatal("logical symlink identity missing")
	}
	for name, target := range map[string]string{"dir": outside, "dangling": filepath.Join(outside, "missing")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	got = Discover(a, root)
	if got.Complete || len(got.Issues) != 0 || len(got.Skipped) != 2 {
		t.Fatalf("Discover aliases = %+v", got)
	}
	for _, reason := range []string{"symlink_directory", "symlink_unresolved"} {
		if !slices.ContainsFunc(got.Skipped, func(s SkippedFile) bool { return s.Reason == reason }) {
			t.Errorf("missing skip reason %s", reason)
		}
	}
	if err := os.Symlink("cycle", filepath.Join(root, "cycle")); err != nil {
		t.Fatal(err)
	}
	got = Discover(a, root)
	if got.Complete || len(got.Issues) == 0 {
		t.Fatal("cyclic alias not reported as incomplete")
	}
}

func TestNoiseDatabasesAreNotExportedEvenWhenExplicitlyRequested(t *testing.T) {
	a := NewCodex(t.TempDir())
	root := a.CanonicalRoot()
	var paths []string
	for _, name := range []string{"logs_1.sqlite", "logs_1.sqlite.before-upgrade", ".codex-global-state.json.bak"} {
		path := filepath.Join(root, name)
		writeSource(t, path, "not a database")
		paths = append(paths, path)
	}
	got := SQLiteExports(t.Context(), a, root, paths, t.TempDir(), time.Hour)
	if !got.Complete || len(got.Files) != 0 || len(got.Issues) != 0 {
		t.Fatalf("SQLiteExports = %+v", got)
	}
}

func TestKnownMarketplacesRemainsRawButNotASnapshotRule(t *testing.T) {
	a := NewClaude(t.TempDir())
	root := a.CanonicalRoot()
	path := filepath.Join(root, "plugins", "known_marketplaces.json")
	const body = "{ \"layout\": true }\n"
	writeSource(t, path, body)
	if slices.Contains(a.Rules().User, "plugins/known_marketplaces.json") {
		t.Fatal("marketplaces still in snapshot rules")
	}
	got := Discover(a, root)
	if !slices.ContainsFunc(got.Files, func(sf SessionFile) bool { return sf.Path == path }) {
		t.Fatal("marketplaces omitted from raw discovery")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatal("raw bytes changed")
	}
}
