package adapters

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiStructuralIdentity(t *testing.T) {
	home := t.TempDir()
	p := NewPi(home)
	const parent = "2026-10-29T00-00-00Z_11111111-1111-1111-1111-111111111111"
	for _, layout := range []string{"flat", "agent", "canonical"} {
		t.Run(layout, func(t *testing.T) {
			root := filepath.Join(home, layout)
			prefix := ""
			if layout == "agent" {
				prefix = "sessions"
			}
			if layout == "canonical" {
				root = p.CanonicalRoot()
				prefix = "sessions"
			}
			for _, local := range []string{parent + ".jsonl", filepath.Join(parent, "tasks", "child.jsonl"), filepath.Join(parent, "forks", "child.jsonl"), filepath.Join(parent, "22222222-2222-2222-2222-222222222222", "run-1", "session.jsonl")} {
				path := filepath.Join(root, prefix, local)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				before, ok := p.Classify(path, root)
				if !ok {
					t.Fatal("unclassified")
				}
				expected := RoleSubagent
				if local == parent+".jsonl" {
					expected = RoleMain
				}
				if before.Role != expected {
					t.Fatalf("role=%s want %s", before.Role, expected)
				}
				logical, ok := ClassifyLogical(p, path, root)
				if !ok || logical != before {
					t.Fatalf("ordinary/logical mismatch: %#v %#v", before, logical)
				}
				// An unrelated sibling directory must not change namespace or role.
				sibling := filepath.Join(root, "sessions")
				if layout == "flat" {
					if err := os.MkdirAll(sibling, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				after, ok := p.Classify(path, root)
				if !ok || after != before {
					t.Fatalf("sibling mkdir changed identity: %#v -> %#v", before, after)
				}
				if layout == "flat" {
					if err := os.Remove(sibling); err != nil {
						t.Fatal(err)
					}
				}
				after, ok = p.Classify(path, root)
				if !ok || after != before {
					t.Fatalf("sibling removal changed identity: %#v -> %#v", before, after)
				}
			}
		})
	}
}
