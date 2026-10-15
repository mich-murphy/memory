package state

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
)

func TestManifestInfoNeverLabelsChildIdentityAsParent(t *testing.T) {
	child := File{Role: adapters.RoleSubagent, Head: map[string]any{"session_id": "child", "agent_id": "agent", "cwd": "/child", "agent_version": "new", "model": "child-model", "started_at": "child-start", "last_event_at": "child-end", "lineage": map[string]any{"relation": "task", "parent_session_id": "parent"}}}
	info := map[string]any{"session_id": "stale-child", "model": "stale", "started_at": "stale"}
	MergeFileInfo(info, []File{child})
	want := map[string]any{"cwd": "/child", "agent_version": "new"}
	if diff := cmp.Diff(want, info); diff != "" {
		t.Errorf("MergeFileInfo (-want +got):\n%s", diff)
	}
	main := File{Role: adapters.RoleMain, Head: map[string]any{"session_id": "parent", "cwd": "/parent", "model": "parent-model", "metadata_version": 1}}
	MergeFileInfo(info, []File{child, main})
	want = map[string]any{"session_id": "parent", "cwd": "/parent", "model": "parent-model"}
	if diff := cmp.Diff(want, info); diff != "" {
		t.Errorf("main MergeFileInfo (-want +got):\n%s", diff)
	}
	if child.Head["session_id"] != "child" || child.Head["lineage"] == nil {
		t.Fatal("MergeFileInfo mutated child head")
	}
	info = map[string]any{"cwd": "/launch"}
	MergeFileInfo(info, []File{child})
	if info["cwd"] != "/launch" {
		t.Errorf("fallback cwd = %v, want launch cwd", info["cwd"])
	}
}
