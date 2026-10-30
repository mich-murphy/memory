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

// Top-level logs are explicit agent resources, including custom agent roots.
// Their identity must not depend on the sessions sibling or root availability.
func TestPiStructuralLogResource(t *testing.T) {
	for _, layout := range []string{"custom", "canonical", "environment"} {
		t.Run(layout, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PI_CODING_AGENT_DIR", "")
			root := filepath.Join(home, "custom-agent")
			if layout == "canonical" {
				root = filepath.Join(home, ".pi", "agent")
			}
			if layout == "environment" {
				root = filepath.Join(home, "env-agent")
				t.Setenv("PI_CODING_AGENT_DIR", root)
			}
			p := NewPi(home)
			path := filepath.Join(root, "pi-debug.log")
			if err := os.MkdirAll(filepath.Join(root, "sessions", "--project--"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("synthetic log\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			key := "roots/" + rootTag(root) + "/pi-debug.log"
			if layout == "canonical" {
				key = "pi-debug.log"
			}
			expected := SessionFile{Agent: "pi", Root: root, Path: path, Key: key, SessionKey: "", Role: RoleLog}
			check := func(stage string) {
				t.Helper()
				ordinary, ok := p.Classify(path, root)
				if !ok || ordinary != expected {
					t.Fatalf("%s: got %#v want %#v", stage, ordinary, expected)
				}
				logical, ok := ClassifyLogical(p, path, root)
				if !ok || logical != expected {
					t.Fatalf("%s logical: got %#v want %#v", stage, logical, expected)
				}
			}
			check("sessions present")
			if err := os.RemoveAll(filepath.Join(root, "sessions")); err != nil {
				t.Fatal(err)
			}
			check("sessions removed")
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			check("logical root absent")
		})
	}
}
