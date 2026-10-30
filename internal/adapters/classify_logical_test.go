package adapters

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiDeclaredLogicalLayoutSurvivesRootMove(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "custom-agent")
	if err := os.MkdirAll(filepath.Join(root, "sessions", "--project--"), 0o700); err != nil {
		t.Fatal(err)
	}
	adapter := NewPi(home)
	paths := []string{filepath.Join(root, "sessions", "--project--", "main.jsonl"), filepath.Join(root, "settings.json"), filepath.Join(root, "skills", "test", "SKILL.md")}
	expected := make([]SessionFile, len(paths))
	for i, path := range paths {
		sf, ok := adapter.Classify(path, root)
		if !ok {
			t.Fatal("legacy fixture unclassified")
		}
		expected[i] = sf
	}
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		sf, ok := ClassifyLogical(adapter, path, root)
		if !ok || sf.Key != expected[i].Key || sf.SessionKey != expected[i].SessionKey || sf.Role != expected[i].Role {
			t.Fatal("logical root availability changed declared source coordinates")
		}
	}
	flat := filepath.Join(home, "flat")
	path := filepath.Join(flat, "plain.jsonl")
	old, ok := adapter.Classify(path, flat)
	if !ok {
		t.Fatal("flat unclassified")
	}
	logical, ok := ClassifyLogical(adapter, path, flat)
	if !ok || old.Key != logical.Key {
		t.Fatal("flat session namespace changed")
	}
	if _, ok := ClassifyLogical(adapter, home, flat); ok {
		t.Fatal("outside root classified")
	}
	claude := NewClaude(home)
	path = filepath.Join(claude.CanonicalRoot(), "projects", "test", "main.jsonl")
	old, ok = claude.Classify(path, claude.CanonicalRoot())
	if !ok {
		t.Fatal("Claude fixture unclassified")
	}
	logical, ok = ClassifyLogical(claude, path, claude.CanonicalRoot())
	if !ok || old.Key != logical.Key {
		t.Fatal("non-Pi identity changed")
	}
}
