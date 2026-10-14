package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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
		{"pi", "sessions/--w--/notes.txt", RoleUnknown, ""},
	} {
		a := all[tc.agent]
		root := a.CanonicalRoot()
		sf, ok := a.Classify(filepath.Join(root, filepath.FromSlash(tc.rel)), root)
		if !ok || sf.Role != tc.role || sf.SessionKey != tc.session {
			t.Errorf("%s %s = %q %q %v, want %q %q", tc.agent, tc.rel, sf.Role, sf.SessionKey, ok, tc.role, tc.session)
		}
	}
}

func TestDiscoverLeavesOutCredentialFiles(t *testing.T) {
	a := NewCodex(t.TempDir())
	root := a.CanonicalRoot()
	for _, name := range []string{"config.toml", "env.md", ".env", ".env.local", "auth.json", "sessions/x.pem"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("OPENAI_API_KEY=sk-1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := Discover(a, root)
	var keys []string
	for _, sf := range files {
		keys = append(keys, sf.Key)
	}
	slices.Sort(keys)
	if diff := cmp.Diff([]string{"config.toml", "env.md"}, keys); diff != "" {
		t.Errorf("Discover keys (-want +got):\n%s", diff)
	}
}

// Keys are the bucket layout (docs/storage-layout.md): a second agent directory is keyed under
// roots/, and Pi sessions from a directory outside the agent dir under dirs/, so neither can
// overwrite the default directory's files. A path that isn't under its root gets no key.
func TestKeysKeepEachRootApart(t *testing.T) {
	other, sessionDir := t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", other) // an agent dir with no sessions/ yet
	all := All(t.TempDir())
	for _, tc := range []struct {
		agent, root, rel string // root "" is the agent's canonical root
		key              string // "" when the file isn't classified
	}{
		{"claude", "", "history.jsonl", "history.jsonl"},
		{"claude", other, "history.jsonl", "roots/" + rootTag(other) + "/history.jsonl"},
		{"codex", "", "history.jsonl", "history.jsonl"},
		{"codex", other, "history.jsonl", "roots/" + rootTag(other) + "/history.jsonl"},
		{"pi", "", "settings.json", "settings.json"},
		{"pi", other, "settings.json", "roots/" + rootTag(other) + "/settings.json"},
		{"pi", sessionDir, "--w--/s.jsonl", "dirs/" + rootTag(sessionDir) + "/--w--/s.jsonl"},
		{"claude", "", "../outside.jsonl", ""},
		{"codex", "", ".", ""},
	} {
		a := all[tc.agent]
		root := tc.root
		if root == "" {
			root = a.CanonicalRoot()
		}
		sf, ok := a.Classify(filepath.Join(root, filepath.FromSlash(tc.rel)), root)
		if sf.Key != tc.key || ok != (tc.key != "") {
			t.Errorf("%s %s under %s: key %q, %v; want %q", tc.agent, tc.rel, root, sf.Key, ok, tc.key)
		}
	}
}

func TestReadHeadReadsTheFirstLinesThatAreObjects(t *testing.T) {
	data := []byte("{\"a\": 1}\n\n  \nnot json\n[1]\n{\"b\": 2}\n{\"c\": 3}\n")
	want := []map[string]any{{"a": json.Number("1")}, {"b": json.Number("2")}}
	if diff := cmp.Diff(want, ReadHead(data, 6)); diff != "" {
		t.Errorf("ReadHead (-want +got):\n%s", diff)
	}
}

func TestPiSessionInfoComesFromTheSessionHeader(t *testing.T) {
	head := []map[string]any{
		{"type": "session", "version": json.Number("3"), "id": "s1", "cwd": "/w", "parentSession": ""},
		{"type": "message", "id": "m1", "cwd": "/elsewhere"},
	}
	want := map[string]any{"session_id": "s1", "cwd": "/w", "format_version": json.Number("3")}
	if diff := cmp.Diff(want, NewPi(t.TempDir()).SessionInfo(head)); diff != "" {
		t.Errorf("SessionInfo (-want +got):\n%s", diff)
	}
}
