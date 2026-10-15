package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	piParent = "2026-10-11T12-34-56.789Z_11111111-2222-3333-4444-555555555555"
	childID  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func TestPiChildLayoutsKeepParentNamespace(t *testing.T) {
	home := t.TempDir()
	p := NewPi(home)
	custom, external := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(custom, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{p.CanonicalRoot(), custom, external} {
		base, prefix := "sessions/--work--/", keyPrefix(p, root)
		if root == external {
			base, prefix = "--work--/", "dirs/"+rootTag(root)+"/"
		}
		for _, tc := range []struct {
			name, suffix string
			role         Role
			grouped      bool
		}{
			{"task", piParent + "/tasks/task-1.jsonl", RoleSubagent, true},
			{"fork", piParent + "/forks/fork-1.jsonl", RoleSubagent, true},
			{"run", piParent + "/" + childID + "/run-2/session.jsonl", RoleSubagent, true},
			{"runZero", piParent + "/" + childID + "/run-0/session.jsonl", RoleSubagent, true},
			{"taskOutput", piParent + "/tasks/task-1.md", RoleAux, true},
			{"runOutput", piParent + "/" + childID + "/run-2/stderr.txt", RoleAux, true},
			{"nested", piParent + "/arbitrary/nested.jsonl", RoleAux, true},
			{"badRun", piParent + "/not-a-uuid/run-2/session.jsonl", RoleAux, true},
			{"orphan", "subagent-artifacts/tasks/task-1.jsonl", RoleAux, false},
			{"unproven", "arbitrary/tasks/task-1.jsonl", RoleAux, false},
		} {
			t.Run(filepath.Base(root)+tc.name, func(t *testing.T) {
				path := filepath.Join(root, filepath.FromSlash(base+tc.suffix))
				sf, ok := p.Classify(path, root)
				wantSession := ""
				if tc.grouped {
					wantSession = prefix + "--work--/" + piParent
				}
				if !ok || sf.Role != tc.role || sf.SessionKey != wantSession || sf.Key != prefix+base+tc.suffix {
					t.Fatalf("Classify = %+v, %v; want role %s session %q", sf, ok, tc.role, wantSession)
				}
				if tc.role == RoleSubagent {
					if got := p.InferRoot(path); got != root {
						t.Errorf("InferRoot = %q, want %q", got, root)
					}
					info := SessionHead(p, sf, []map[string]any{{"type": "session", "id": childID}}, nil)
					relation := tc.name
					if tc.name == "runZero" {
						relation = "run"
					}
					want := map[string]any{"relation": relation, "parent_session_id": "11111111-2222-3333-4444-555555555555"}
					if diff := cmp.Diff(want, info["lineage"]); diff != "" {
						t.Errorf("lineage (-want +got):\n%s", diff)
					}
					if info["session_id"] != childID {
						t.Errorf("session_id = %v, want own child id", info["session_id"])
					}
				}
			})
		}
	}
}

func TestPiRelatedIncludesParentAndAllChildren(t *testing.T) {
	p := NewPi(t.TempDir())
	root := p.CanonicalRoot()
	parent := filepath.Join(root, "sessions", "--work--", piParent)
	paths := []string{parent + ".jsonl", filepath.Join(parent, "tasks", "task.jsonl"), filepath.Join(parent, childID, "run-1", "session.jsonl"), filepath.Join(parent, "tasks", "output.md")}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sf, ok := p.Classify(paths[1], root)
	if !ok {
		t.Fatal("Classify rejected child")
	}
	got := p.Related(sf)
	if !got.Complete || len(got.Files) != len(paths) {
		t.Fatalf("Related = %+v, want %d files", got, len(paths))
	}
}

func TestHeadsUseObservedIdentityLineageAndTimes(t *testing.T) {
	all := All(t.TempDir())
	for _, tc := range []struct {
		name, agent, body string
		want              map[string]any
	}{
		{"piFork", "pi", `{"type":"session","id":"child","timestamp":"2026-10-11T14:00:00+02:00","parentSession":"/sessions/--work--/` + piParent + `.jsonl"}
{"type":"model_change","timestamp":"2026-10-11T12:00:01Z","modelId":"head-model"}
{"type":"message","timestamp":"2026-10-11T12:00:02Z","message":{"role":"assistant","model":"latest-model"}}`, map[string]any{"session_id": "child", "parent_session": "/sessions/--work--/" + piParent + ".jsonl", "started_at": "2026-10-11T12:00:00Z", "last_event_at": "2026-10-11T12:00:02Z", "model": "latest-model", "lineage": map[string]any{"relation": "fork", "parent_session_id": "11111111-2222-3333-4444-555555555555"}}},
		{"claudeChild", "claude", `{"type":"user","agentId":"agent-own","sessionId":"parent","timestamp":"2026-10-11T12:00:00Z"}
{"type":"assistant","timestamp":9007199254740993,"message":{"model":"claude-latest"}}`, map[string]any{"session_id": "parent", "agent_id": "agent-own", "started_at": "2026-10-11T12:00:00Z", "last_event_at": json.Number("9007199254740993"), "model": "claude-latest", "lineage": map[string]any{"relation": "subagent", "parent_session_id": "parent"}}},
		{"piUnknownParent", "pi", `{"type":"session","id":"own","parentSession":"/arbitrary/file.jsonl"}`, map[string]any{"session_id": "own", "parent_session": "/arbitrary/file.jsonl"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := all[tc.agent].SessionInfo(ReadHead([]byte(tc.body), 20))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("SessionInfo (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCodexHeadRejectsReplayedParentMetadata(t *testing.T) {
	c := NewCodex(t.TempDir())
	path := filepath.Join(c.CanonicalRoot(), "sessions", "2026", "10", "11", "rollout-2026-10-11T12-00-00-"+childID+".jsonl")
	sf, ok := c.Classify(path, c.CanonicalRoot())
	if !ok {
		t.Fatal("Classify rejected rollout")
	}
	parent := map[string]any{"type": "session_meta", "timestamp": "2020-01-01T00:00:00Z", "payload": map[string]any{"id": "parent", "cwd": "/parent", "cli_version": "old"}}
	own := map[string]any{"type": "session_meta", "timestamp": "2026-10-11T12:00:00Z", "payload": map[string]any{"id": childID, "cwd": "/own", "cli_version": "new", "forked_from_id": "parent"}}
	for _, tc := range []struct {
		name       string
		head, tail []map[string]any
	}{
		{"ownFirst", []map[string]any{own, parent}, nil},
		{"ownLast", []map[string]any{parent, own}, nil},
		{"ownInTail", []map[string]any{parent}, []map[string]any{own}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SessionHead(c, sf, tc.head, tc.tail)
			if got["session_id"] != childID || got["cwd"] != "/own" || got["started_at"] != "2026-10-11T12:00:00Z" {
				t.Fatalf("SessionHead = %v", got)
			}
			want := map[string]any{"relation": "fork", "parent_session_id": "parent"}
			if diff := cmp.Diff(want, got["lineage"]); diff != "" {
				t.Error(diff)
			}
		})
	}
	missing := SessionHead(c, sf, []map[string]any{parent}, nil)
	for _, key := range []string{"session_id", "cwd", "started_at", "lineage"} {
		if _, ok := missing[key]; ok {
			t.Errorf("missing own header claims %s = %v", key, missing[key])
		}
	}
	own["payload"].(map[string]any)["source"] = map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "thread-parent"}}}
	got := SessionHead(c, sf, []map[string]any{own, parent}, nil)
	want := map[string]any{"relation": "child_thread", "parent_session_id": "thread-parent"}
	if diff := cmp.Diff(want, got["lineage"]); diff != "" {
		t.Error(diff)
	}
}

func TestTailCannotInventSessionStart(t *testing.T) {
	for name, a := range All(t.TempDir()) {
		kind := "message"
		if name == "claude" {
			kind = "user"
		}
		if name == "codex" {
			kind = "event_msg"
		}
		got := SessionHead(a, SessionFile{Agent: name}, nil, ReadHead([]byte(`{"type":"`+kind+`","timestamp":"2026-10-11T12:00:00Z"}`), 1))
		if _, ok := got["started_at"]; ok {
			t.Errorf("%s started_at = %v, want absent", name, got["started_at"])
		}
		if !strings.HasSuffix(got["last_event_at"].(string), "Z") {
			t.Errorf("last_event_at = %v", got["last_event_at"])
		}
	}
}

func TestPiFlatSessionDirectoriesHaveConsistentHookKeys(t *testing.T) {
	p := NewPi(t.TempDir())
	for _, root := range []string{p.CanonicalRoot(), t.TempDir()} {
		base := root
		if root == p.CanonicalRoot() {
			base = filepath.Join(root, "sessions")
		}
		for _, rel := range []string{piParent + ".jsonl", piParent + "/tasks/child.jsonl", piParent + "/" + childID + "/run-1/session.jsonl"} {
			path := filepath.Join(base, filepath.FromSlash(rel))
			sf, ok := p.Classify(path, root)
			if !ok || sf.SessionKey == "" {
				t.Fatalf("Classify = %+v, %v", sf, ok)
			}
			if got := p.InferRoot(path); got != root {
				t.Errorf("InferRoot = %q, want %q", got, root)
			}
			if strings.Contains(rel, "/") && sf.Role != RoleSubagent {
				t.Errorf("Role = %s, want subagent", sf.Role)
			}
		}
	}
}

func TestPiUnmarkedNestedHookPathsDoNotInventMainSessions(t *testing.T) {
	p := NewPi(t.TempDir())
	for _, rel := range []string{
		"subagent-artifacts/tasks/child.jsonl",
		"subagent-artifacts/" + piParent + ".jsonl",
		"arbitrary/nested.jsonl",
		"--work--/" + piParent + "/arbitrary/nested.jsonl",
	} {
		if got := p.InferRoot(filepath.Join(t.TempDir(), filepath.FromSlash(rel))); got != "" {
			t.Errorf("InferRoot(%q) = %q, want no proven session root", rel, got)
		}
	}
}
