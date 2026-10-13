package adapters

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzSessionInfo feeds arbitrary transcript heads to every agent's parser: a malformed or
// hostile first line must never stop a sync.
func FuzzSessionInfo(f *testing.F) {
	for _, seed := range []string{
		`{"type":"session","id":"x","cwd":"/w","version":3}` + "\n",
		`{"type":"session_meta","payload":{"id":"t","cwd":"/w","git":{"branch":"main"}}}` + "\n",
		`{"type":"user","sessionId":"s","cwd":"/w","gitBranch":"main","version":"2.1"}` + "\n" + `{"type":"assistant"`,
		`{"payload":null}` + "\n" + `[1,2]` + "\n" + `{"cwd":7}`,
		"\n\n  \n",
	} {
		f.Add([]byte(seed))
	}
	all := All(f.TempDir())
	f.Fuzz(func(t *testing.T, data []byte) {
		head := ReadHead(data, 5)
		if len(head) > 5 {
			t.Fatalf("ReadHead returned %d lines", len(head))
		}
		for _, name := range Names {
			_ = all[name].SessionInfo(head)
		}
	})
}

func TestClassifyRoles(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	rollout := "rollout-2026-01-02T03-04-05-" + id + ".jsonl"
	all := All(t.TempDir())
	for _, tc := range []struct {
		agent, rel string
		role       Role
		session    string
	}{
		{"claude", "history.jsonl", RoleHistory, ""},
		{"claude", "settings.json", RoleConfig, ""},
		{"claude", "statusline.sh", RoleConfig, ""},
		{"claude", "todos", RoleUnknown, ""},
		{"claude", "notes.txt", RoleUnknown, ""},
		{"claude", "plans/p.md", RolePlan, ""},
		{"claude", "sessions/123.json", RoleLiveness, ""},
		{"claude", "todos/" + id + "-agent.json", RoleTodo, ""},
		{"claude", "projects/slug/" + id + ".jsonl", RoleMain, "slug/" + id},
		{"claude", "projects/slug/" + id + "/subagents/a.jsonl", RoleSubagent, "slug/" + id},
		{"claude", "projects/slug/memory/m.md", RoleMemory, ""},
		{"codex", "history.jsonl", RoleHistory, ""},
		{"codex", "config.toml", RoleConfig, ""},
		{"codex", "skills", RoleUnknown, ""},
		{"codex", "memories/m.md", RoleMemory, ""},
		{"codex", "log/codex.log", RoleLog, ""},
		{"codex", "sessions/notes.txt", RoleUnknown, ""},
		{"codex", "sessions/2026/01/02/" + rollout, RoleMain, strings.TrimSuffix(rollout, ".jsonl")},
		{"pi", "settings.json", RoleConfig, ""},
		{"pi", "pi-debug.log", RoleLog, ""},
		{"pi", "themes", RoleUnknown, ""},
		{"pi", "extensions/e.ts", RoleConfig, ""},
		{"pi", "experimental/x.json", RoleExperimental, ""},
		{"pi", "sessions/--w--/s.jsonl", RoleMain, "s"},
	} {
		a := all[tc.agent]
		root := a.CanonicalRoot()
		sf, ok := a.Classify(filepath.Join(root, filepath.FromSlash(tc.rel)), root)
		if !ok || sf.Role != tc.role || sf.SessionKey != tc.session {
			t.Errorf("%s %s = %q %q %v, want %q %q", tc.agent, tc.rel, sf.Role, sf.SessionKey, ok, tc.role, tc.session)
		}
	}
}
