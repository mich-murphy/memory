package adapters

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCodexSessionInfoTakesTheFirstNonEmptyValue(t *testing.T) {
	head := []map[string]any{
		{"type": "session_meta", "payload": map[string]any{"id": "s1", "cwd": "", "git": map[string]any{}}, "git": map[string]any{"branch": "main"}},
		{"type": "turn_context", "payload": map[string]any{"cwd": "/work", "model": ""}},
		{"type": "turn_context", "payload": map[string]any{"cwd": "/elsewhere", "model": "gpt-5"}},
	}
	want := map[string]any{"session_id": "s1", "cwd": "/work", "git_branch": "main", "model": "gpt-5"}
	if diff := cmp.Diff(want, NewCodex(t.TempDir()).SessionInfo(head)); diff != "" {
		t.Errorf("SessionInfo (-want +got):\n%s", diff)
	}
}
