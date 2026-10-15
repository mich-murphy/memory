package state

import (
	"maps"

	"github.com/mich-murphy/memory/internal/adapters"
)

// MergeFileInfo projects parent/session metadata, not every transcript's identity.
// Child heads remain intact in transcript entries even when the main is absent.
func MergeFileInfo(info map[string]any, files []File) {
	// Replaying a manifest must not keep obsolete main/child identity or observations.
	for _, key := range []string{"session_id", "agent_id", "root_session_id", "parent_session", "forked_from", "lineage", "started_at", "last_event_at", "model", "agent_role", "metadata_version", "agent_version", "format_version", "entrypoint", "originator", "git_branch", "git_commit", "git_remote"} {
		delete(info, key)
	}
	for i := range files {
		file := &files[i]
		if file.Role != adapters.RoleMain {
			continue
		}
		maps.Copy(info, file.Head)
		delete(info, "metadata_version")
		return
	}
	// These fields describe the harness/location as observed by a child, not the
	// absent parent's identity, start time, event time, model or provenance.
	for i := range files {
		file := &files[i]
		if file.Role != adapters.RoleSubagent {
			continue
		}
		for _, key := range []string{"cwd", "agent_version", "format_version", "entrypoint", "originator"} {
			if _, exists := info[key]; exists {
				continue
			}
			if value, known := file.Head[key]; known {
				info[key] = value
			}
		}
	}
}
