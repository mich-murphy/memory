package exclusion

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
)

func TestOptOutPersistsAndCoversRelatedAndSharedSources(t *testing.T) {
	home := t.TempDir()
	a := adapters.NewClaude(home)
	dir := t.TempDir()
	id := "11111111-2222-3333-4444-555555555555"
	path := filepath.Join(a.CanonicalRoot(), "projects/slug/"+id+".jsonl")
	r, err := RecordPath(dir, a, path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry) != 1 {
		t.Fatalf("Load = %+v", registry)
	}
	for _, rel := range []string{"projects/slug/" + id + ".jsonl", "projects/slug/" + id + "/subagents/a.jsonl", "history.jsonl", "memories/x.md"} {
		sf, ok := a.Classify(filepath.Join(a.CanonicalRoot(), rel), a.CanonicalRoot())
		if !ok || !registry.Excludes(sf) {
			t.Errorf("not excluded: %s", rel)
		}
	}
	sf, _ := a.Classify(filepath.Join(a.CanonicalRoot(), "projects", "slug", "other.jsonl"), a.CanonicalRoot())
	if registry.Excludes(sf) {
		t.Fatal("unrelated session excluded")
	}
	if !registry.Excludes(adapters.SessionFile{Agent: "claude", Root: a.CanonicalRoot(), Path: filepath.Join(a.CanonicalRoot(), "state.sqlite")}) {
		t.Fatal("database not excluded")
	}
	sf.Agent = "pi"
	if registry.Excludes(sf) {
		t.Fatal("different agent excluded")
	}
	if !registry.ExcludesPath(a, path) {
		t.Fatal("reported path not excluded")
	}
	if registry.ExcludesPath(a, "/unrecognised") {
		t.Fatal("unknown path excluded")
	}
	recovered := t.TempDir()
	if err := Save(recovered, r); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(recovered); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "exclusions", r.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o", st.Mode().Perm())
	}
}

func TestRootAliasesAndLateCreatedPathsCannotEvadeOptOut(t *testing.T) {
	home := t.TempDir()
	a := adapters.NewPi(home)
	physicalRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(a.CanonicalRoot()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physicalRoot, a.CanonicalRoot()); err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(physicalRoot, "sessions", "--w--", "new.jsonl")
	r, err := RecordPath(t.TempDir(), a, physical)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root != a.CanonicalRoot() {
		t.Fatalf("logical root = %s", r.Root)
	}
	registry := Registry{r}
	sf := adapters.SessionFile{Agent: "pi", Root: physicalRoot, Path: physical, SessionKey: "roots/alias/--w--/new"}
	if !registry.Excludes(sf) {
		t.Fatal("physical alias not excluded")
	}
	export := adapters.SessionFile{Agent: "pi", Root: "/different", Path: "/generated", SourcePath: physical}
	if !registry.Excludes(export) {
		t.Fatal("export alias not excluded")
	}
	if err := os.Remove(a.CanonicalRoot()); err != nil {
		t.Fatal(err)
	}
	if err := Save(t.TempDir(), r); err != nil {
		t.Fatalf("stable record after link removal: %v", err)
	}
}

func TestRegistryFailuresAreExplicit(t *testing.T) {
	a := adapters.NewPi(t.TempDir())
	if _, err := RecordPath(t.TempDir(), a, "/unknown"); err == nil {
		t.Fatal("unknown identity accepted")
	}
	if err := Save(t.TempDir(), Record{}); err == nil {
		t.Fatal("invalid record accepted")
	}
	dir := t.TempDir()
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "exclusions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exclusions", "bad.json"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt registry accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "exclusions", "bad.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("invalid registry accepted")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordPath(blocked, a, filepath.Join(a.CanonicalRoot(), "sessions", "--w--", "x.jsonl")); err == nil {
		t.Fatal("write failure hidden")
	}
	if _, err := Load(blocked); err == nil {
		t.Fatal("read failure hidden")
	}
}
