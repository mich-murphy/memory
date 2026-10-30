package capturesource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeAbsentLayoutAndBackupGuard(t *testing.T) {
	o := options(t)
	root := filepath.Join(o.Home, ".claude")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "projects", "p", "s.jsonl")
	if _, err := Classify(t.Context(), o, "claude", path); err == nil {
		t.Fatal("ordinary accepted absent file")
	}
	req, err := ClassifyNative(t.Context(), o, "claude", path)
	if err != nil || req.Role != "main" {
		t.Fatal(req, err)
	}
	if _, err := ClassifyNative(t.Context(), o, "claude", filepath.Join(root, "debug", "s.txt")); err == nil {
		t.Fatal("external accepted")
	}
	backup := fixture(t, filepath.Join(root, "settings.json.agent-sessions-capture-v1-backup-123"), "private")
	for _, derived := range []bool{false, true} {
		o.Derived = derived
		if _, err := Classify(t.Context(), o, "claude", backup); err == nil {
			t.Fatal("unsafe backup admitted")
		}
		result := discover(t, o)
		for _, q := range result.Requests {
			if q.Path == backup {
				t.Fatal("unsafe backup discovered raw")
			}
		}
	}
}
