// Package testenv builds the fake home the collector's tests run against: Claude Code, Codex
// and Pi sessions for one git project, plus the credentials and noise that must never be
// copied.
package testenv

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Session ids used throughout the fixture.
const (
	ClaudeSID   = "11111111-2222-3333-4444-555555555555"
	CodexThread = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	PiSID       = "0199a1b2-aaaa-7bbb-8ccc-dddddddddddd"
	MachineID   = "testbox"
	Slug        = "-home-code-proj"
	ClaudeKey   = Slug + "/" + ClaudeSID
	CodexKey    = "rollout-2026-10-10T10-00-00-" + CodexThread
	PiKey       = "2026-10-10T10-00-00-000Z_" + PiSID
)

// Env is a fake home and the collector's directories for one test.
type Env struct {
	T            testing.TB
	Tmp          string
	Home         string
	Project      string
	Bucket       string
	State        string
	Claude       string
	ClaudeSlug   string
	CodexRollout string
	PiDir        string
}

// JSONL renders objects one per line.
func JSONL(t testing.TB, objs ...any) string {
	t.Helper()
	var b strings.Builder
	for _, o := range objs {
		line, err := json.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// Write creates path (and its parents) holding text.
func Write(t testing.TB, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Append adds text to the end of path.
func Append(t testing.TB, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// Settle sets path's mtime age ago.
func Settle(t testing.TB, path string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// Git runs git in dir.
func Git(t testing.TB, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// New builds the fake home and points the collector's environment at it. Tests using it can't
// run in parallel, since it sets environment variables.
func New(t testing.TB) *Env {
	t.Helper()
	tmp := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved // macOS: /var is a symlink to /private/var
	}
	home := filepath.Join(tmp, "home")
	for _, v := range []string{
		"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "AGENT_SESSIONS_DISABLE",
		"XDG_CONFIG_HOME",
	} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v) // t.Setenv restores the old value afterwards
	}
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(tmp, "config.env"))
	t.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Join(tmp, "state"))
	t.Setenv("AGENT_SESSIONS_LOCAL_STORE", filepath.Join(tmp, "bucket"))
	t.Setenv("AGENT_SESSIONS_MACHINE_ID", MachineID)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	e := &Env{
		T: t, Tmp: tmp, Home: home, Project: filepath.Join(home, "code", "proj"), Bucket: filepath.Join(tmp, "bucket"),
		State: filepath.Join(tmp, "state"), Claude: filepath.Join(home, ".claude"),
	}
	e.project()
	e.claude()
	e.codex()
	e.pi()

	// Make everything look settled (older than the partial-line window).
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		Settle(t, p, time.Hour)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *Env) project() {
	t, p := e.T, e.Project
	if err := os.MkdirAll(filepath.Join(p, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "-b", "main", p).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	Write(t, filepath.Join(p, "CLAUDE.md"), "# proj rules\n@docs/conventions.md\n")
	Write(t, filepath.Join(p, "docs", "conventions.md"), "use tabs\n")
	Write(t, filepath.Join(p, "AGENTS.md"), "# agents rules\n")
	Write(t, filepath.Join(p, "src", "CLAUDE.md"), "nested\n")
	Write(t, filepath.Join(p, ".claude", "settings.json"), `{"env": {"DB_PASSWORD": "hunter2"}}`)
	Write(t, filepath.Join(e.Home, "code", "CLAUDE.md"), "ancestor rules\n")
	Git(t, p, "remote", "add", "origin", "https://user:ghp_token@github.com/me/proj.git")
	Git(t, p, "add", "-A")
	Git(t, p, "commit", "-q", "-m", "first")
}

func (e *Env) claude() {
	t, c := e.T, e.Claude
	e.ClaudeSlug = filepath.Join(c, "projects", Slug)
	slug, sid := e.ClaudeSlug, ClaudeSID
	Write(t, filepath.Join(slug, sid+".jsonl"), JSONL(t,
		map[string]any{
			"type": "user", "sessionId": sid, "cwd": e.Project, "gitBranch": "main", "version": "2.1.296",
			"uuid": "u1", "parentUuid": nil, "message": map[string]any{"role": "user", "content": "hi"},
		},
		map[string]any{
			"type": "assistant", "sessionId": sid, "uuid": "a1", "parentUuid": "u1",
			"message": map[string]any{"role": "assistant", "content": "hello"},
		},
	))
	Write(t, filepath.Join(slug, sid, "subagents", "agent-abc.jsonl"), JSONL(t, map[string]any{"type": "user", "isSidechain": true}))
	Write(t, filepath.Join(slug, sid, "subagents", "agent-abc.meta.json"), "{}")
	Write(t, filepath.Join(slug, sid, "tool-results", "toolu_1.txt"), "big output\n")
	Write(t, filepath.Join(slug, sid+".jsonl.superseded-20261001"), JSONL(t, map[string]any{"type": "user"}))
	Write(t, filepath.Join(slug, "memory", "MEMORY.md"), "- remember this\n")
	Write(t, filepath.Join(c, "plans", "brave-plan.md"), "# plan\n")
	Write(t, filepath.Join(c, "history.jsonl"), JSONL(t, map[string]any{"display": "hi", "project": e.Project}))
	Write(t, filepath.Join(c, "hooks", "guard.sh"), "#!/bin/sh\nexit 0\n")
	Write(t, filepath.Join(e.Home, "bin", "status.py"), "print('status')\n")
	Write(t, filepath.Join(c, "settings.json"), mustJSON(t, map[string]any{
		"env":   map[string]any{"ANTHROPIC_API_KEY": "sk-ant-xxxxxxxxxxxxxxxxxxxxxxxx"},
		"model": "opus",
		"hooks": map[string]any{"PreToolUse": []any{map[string]any{
			"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "~/.claude/hooks/guard.sh"}},
		}}},
		"statusLine": map[string]any{"type": "command", "command": "python3 " + e.Home + "/bin/status.py"},
	}))
	Write(t, filepath.Join(c, "CLAUDE.md"), "user rules\n")
	Write(t, filepath.Join(c, "skills", "deploy", "SKILL.md"), "---\nname: deploy\n---\nRun scripts/go.sh\n")
	Write(t, filepath.Join(c, "skills", "deploy", "scripts", "go.sh"), "echo go\n")
	Write(t, filepath.Join(c, ".credentials.json"), `{"claudeAiOauth": {"accessToken": "secret-oauth"}}`)
	Write(t, filepath.Join(c, "sessions", "123.key"), "secret-key")
	Write(t, filepath.Join(c, "sessions", "123.json"), `{"pid": 123}`)
	Write(t, filepath.Join(c, "file-history", sid, "abc@v1"), "old contents\n")
	Write(t, filepath.Join(c, "teams", "alpha", "inboxes", "lead.json"), "[]")
	Write(t, filepath.Join(c, "shell-snapshots", "snap.sh"), "export X=1")
	Write(t, filepath.Join(c, "mystery.dat"), "?")
	Write(t, filepath.Join(e.Home, ".claude.json"), mustJSON(t, map[string]any{
		"primaryApiKey": "sk-ant-api-secret",
		"oauthAccount":  map[string]any{"emailAddress": "me@example.com"},
		"mcpServers": map[string]any{"gh": map[string]any{
			"command": "npx", "env": map[string]any{"GITHUB_TOKEN": "ghp_" + strings.Repeat("a", 36)},
		}},
		"projects": map[string]any{e.Project: map[string]any{
			"mcpServers": map[string]any{"local": map[string]any{
				"url": "http://x", "headers": map[string]any{"Authorization": "Bearer abc"},
			}},
			"history": []any{"x"},
		}},
	}))
}

func (e *Env) codex() {
	t := e.T
	codex := filepath.Join(e.Home, ".codex")
	e.CodexRollout = filepath.Join(codex, "sessions", "2026", "10", "10", CodexKey+".jsonl")
	Write(t, e.CodexRollout, JSONL(t,
		map[string]any{
			"timestamp": "2026-10-10T10:00:00Z", "type": "session_meta",
			"payload": map[string]any{
				"id": CodexThread, "session_id": CodexThread, "cwd": e.Project, "cli_version": "0.162.1",
				"originator": "codex_cli_rs", "git": map[string]any{"branch": "main", "commit_hash": "abc"},
			},
		},
		map[string]any{
			"timestamp": "2026-10-10T10:00:01Z", "type": "event_msg",
			"payload": map[string]any{"type": "user_message", "message": "hi"},
		},
	))
	Write(t, filepath.Join(codex, "history.jsonl"), JSONL(t, map[string]any{"session_id": CodexThread, "text": "hi"}))
	Write(t, filepath.Join(codex, "config.toml"), "model = \"gpt-5\"\n[mcp_servers.gh.env]\nGITHUB_TOKEN = \"ghp_secret\"\n")
	Write(t, filepath.Join(codex, "AGENTS.md"), "codex user rules\n")
	Write(t, filepath.Join(codex, "auth.json"), `{"OPENAI_API_KEY": "sk-secret"}`)
	Write(t, filepath.Join(codex, "memories", "notes.md"), "codex remembers\n")
}

func (e *Env) pi() {
	t := e.T
	agent := filepath.Join(e.Home, ".pi", "agent")
	e.PiDir = filepath.Join(agent, "sessions", "--home-code-proj--")
	Write(t, filepath.Join(e.PiDir, PiKey+".jsonl"), JSONL(t,
		map[string]any{"type": "session", "version": 3, "id": PiSID, "timestamp": "2026-10-10T10:00:00.000Z", "cwd": e.Project},
		map[string]any{
			"type": "message", "id": "a0b1c2d3", "parentId": nil, "timestamp": "2026-10-10T10:00:01.000Z",
			"message": map[string]any{"role": "user", "content": "hi", "timestamp": 1},
		},
	))
	Write(t, filepath.Join(agent, "AGENTS.md"), "pi user rules\n")
	Write(t, filepath.Join(agent, "auth.json"), `{"anthropic": {"key": "sk-secret"}}`)
	Write(t, filepath.Join(agent, "models.json"), `{"providers": {"corp": {"baseUrl": "https://llm.corp/v1", "apiKey": "sk-pi-literal"}}}`)
}

// BucketKeys lists every object in the local bucket, sorted.
func (e *Env) BucketKeys() []string {
	var keys []string
	err := filepath.WalkDir(e.Bucket, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(e.Bucket, p)
		keys = append(keys, filepath.ToSlash(rel))
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		e.T.Fatal(err)
	}
	slices.Sort(keys)
	return keys
}

// HasKey reports whether the bucket holds key.
func (e *Env) HasKey(key string) bool {
	_, err := os.Stat(filepath.Join(e.Bucket, filepath.FromSlash(key)))
	return err == nil
}

// Raw is the object key of an agent's raw file.
func Raw(agent, key string) string {
	return "raw/agent=" + agent + "/machine=" + MachineID + "/" + key + ".zst"
}

// Manifest reads a session's manifest from the bucket.
func (e *Env) Manifest(agent, sessionKey string) map[string]any {
	e.T.Helper()
	return e.ReadJSON("manifests/agent=" + agent + "/machine=" + MachineID + "/" + sessionKey + ".json")
}

// ReadJSON reads a JSON object from the bucket.
func (e *Env) ReadJSON(key string) map[string]any {
	e.T.Helper()
	data, err := os.ReadFile(filepath.Join(e.Bucket, filepath.FromSlash(key)))
	if err != nil {
		e.T.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		e.T.Fatal(err)
	}
	return m
}
