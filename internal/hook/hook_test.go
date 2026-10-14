package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/testenv"
)

type spawned struct {
	agent, path, event string
	args               []string
}

// fakeSpawn records what would have been started instead of starting it.
func fakeSpawn(t *testing.T) *[]spawned {
	t.Helper()
	var calls []spawned
	old := startSync
	startSync = func(_ context.Context, args ...string) error {
		get := func(flag string) string {
			if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		event, _ := strings.CutPrefix(get("--trigger"), "hook:")
		calls = append(calls, spawned{get("--agent"), get("--path"), event, args})
		return nil
	}
	t.Cleanup(func() { startSync = old })
	return &calls
}

// withStdin runs fn with payload on stdin, as an agent pipes it to a hook.
func withStdin(t *testing.T, payload string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
	fn()
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHookQueuesOneSyncPerTranscriptAndDebounces(t *testing.T) {
	testenv.New(t)
	calls := fakeSpawn(t)
	payload := mustJSON(t, map[string]any{
		"hook_event_name": "SubagentStop", "transcript_path": "/x/main.jsonl",
		"agent_transcript_path": "/x/main/subagents/agent-1.jsonl",
	})
	withStdin(t, payload, func() { Run(t.Context(), "claude", nil, "", ExtensionArgs{}) })
	var got [][3]string
	for _, c := range *calls {
		got = append(got, [3]string{c.agent, c.path, c.event})
	}
	want := [][3]string{
		{"claude", "/x/main.jsonl", "SubagentStop"},
		{"claude", "/x/main/subagents/agent-1.jsonl", "SubagentStop"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("spawned:\n%s", diff)
	}
	withStdin(t, `{"transcript_path": "/x/main.jsonl"}`, func() { Run(t.Context(), "claude", nil, "", ExtensionArgs{}) })
	if len(*calls) != 2 {
		t.Errorf("a Stop while a sync was queued spawned another: %d syncs", len(*calls))
	}
}

func TestStalePendingMarkerIsTakenOver(t *testing.T) {
	e := testenv.New(t)
	calls := fakeSpawn(t)
	marker := claimPending(e.State, "/x/a.jsonl")
	if marker == "" {
		t.Fatal("no marker")
	}
	testenv.Backdate(t, marker, markerStaleAfter+5*time.Second)
	withStdin(t, `{"transcript_path": "/x/a.jsonl"}`, func() { Run(t.Context(), "codex", nil, "", ExtensionArgs{}) })
	if len(*calls) != 1 || (*calls)[0].path != "/x/a.jsonl" || (*calls)[0].event != "codex" {
		t.Errorf("spawned %v", *calls)
	}
}

func TestHookNeverFails(t *testing.T) {
	for _, payload := range []string{"", "not json", "[1,2]", `{"transcript_path": null}`, `{"transcript_path": ""}`} {
		t.Run(payload, func(t *testing.T) {
			testenv.New(t)
			calls := fakeSpawn(t)
			withStdin(t, payload, func() { Run(t.Context(), "codex", nil, "", ExtensionArgs{}) })
			if len(*calls) != 0 {
				t.Errorf("spawned %v", *calls)
			}
		})
	}
}

func TestDisabledHookIsANoop(t *testing.T) {
	testenv.New(t)
	calls := fakeSpawn(t)
	t.Setenv("AGENT_SESSIONS_DISABLE", "1")
	Run(t.Context(), "pi", []string{"/x/s.jsonl"}, "", ExtensionArgs{})
	if len(*calls) != 0 {
		t.Errorf("spawned %v", *calls)
	}
}

func readLaunchFile(t *testing.T, args []string) map[string]any {
	t.Helper()
	i := slices.Index(args, "--launch")
	if i < 0 {
		t.Fatalf("no --launch in %v", args)
	}
	var launch map[string]any
	if err := json.Unmarshal(testReadFile(t, args[i+1]), &launch); err != nil {
		t.Fatal(err)
	}
	return launch
}

func testReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSessionStartRecordsLaunchContext(t *testing.T) {
	testenv.New(t)
	calls := fakeSpawn(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "secret")
	t.Setenv("ANTHROPIC_MODEL", "opus")
	payload := mustJSON(t, map[string]any{
		"hook_event_name": "SessionStart", "source": "startup", "cwd": "/w", "transcript_path": "/x/s.jsonl",
		"permission_mode": "default",
	})
	before := float64(time.Now().Unix())
	withStdin(t, payload, func() { Run(t.Context(), "claude", nil, "", ExtensionArgs{}) })
	after := float64(time.Now().Unix() + 1)
	if len(*calls) != 2 {
		t.Fatalf("spawned %v", *calls)
	}
	c := (*calls)[0]
	if c.path != "/x/s.jsonl" || slices.Contains(c.args, "--pending-marker") {
		t.Errorf("args = %v", c.args)
	}
	launch := readLaunchFile(t, c.args)
	env := launch["env"].(map[string]any)
	if launch["source"] != "startup" || launch["cwd"] != "/w" || launch["permission_mode"] != "default" {
		t.Errorf("launch = %v", launch)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(input, launch["hook_input"]); diff != "" {
		t.Errorf("hook_input (-payload +got):\n%s", diff)
	}
	if at, _ := launch["recorded_at"].(float64); at < before || at > after {
		t.Errorf("recorded_at = %v, want Unix seconds between %v and %v", launch["recorded_at"], before, after)
	}
	if env["CLAUDE_CODE_ENTRYPOINT"] != "cli" || env["ANTHROPIC_MODEL"] != "opus" {
		t.Errorf("env = %v", env)
	}
	if _, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Error("a token was recorded")
	}
	if len(launch["process"].(map[string]any)["ancestry"].([]any)) == 0 {
		t.Error("no ancestry") // the test's own process tree; no claude in it
	}
}

func TestLaunchEnvKeepsTokenLimitsAndDropsSecrets(t *testing.T) {
	got := launchEnv([]string{
		"MAX_THINKING_TOKENS=9000", "MAX_MCP_OUTPUT_TOKENS=5", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=1", "CLAUDE_CODE_OAUTH_TOKEN=s",
		"CODEX_API_KEY=s", "PI_SECRET=s", "CLAUDE_CODE_ENTRYPOINT=cli", "HOME=/h",
	})
	want := map[string]any{
		"MAX_THINKING_TOKENS": "9000", "MAX_MCP_OUTPUT_TOKENS": "5", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "1",
		"CLAUDE_CODE_ENTRYPOINT": "cli",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("launchEnv (-want +got):\n%s", diff)
	}
}

func TestRecordedCommandLinesHaveTheirSecretsRedacted(t *testing.T) {
	process := processFromExtension(`["node", "/usr/lib/pi/dist/cli.js", "--api-key", "sk-1", "--model", "m"]`)
	want := []string{"node", "/usr/lib/pi/dist/cli.js", "--api-key", "[redacted]", "--model", "m"}
	if diff := cmp.Diff(want, process["argv"]); diff != "" {
		t.Errorf("extension argv (-want +got):\n%s", diff)
	}

	oldProc, oldPPID := readProc, parentPID
	readProc = func(int) ([]string, int, bool) {
		return []string{"claude", "--mcp-config", `{"mcpServers":{"s":{"headers":{"Authorization":"Bearer t"}}}}`}, 1, true
	}
	parentPID = func() int { return 10 }
	t.Cleanup(func() { readProc, parentPID = oldProc, oldPPID })
	want = []string{"claude", "--mcp-config", `{"mcpServers":{"s":{"headers":{"Authorization":"[redacted]"}}}}`}
	if diff := cmp.Diff(want, launchProcess("claude")["argv"]); diff != "" {
		t.Errorf("process table argv (-want +got):\n%s", diff)
	}
}

func TestSessionStartsAndEndsQueueOneSweepAtATime(t *testing.T) {
	e := testenv.New(t)
	calls := fakeSpawn(t)
	sweeps := func() []spawned {
		var out []spawned
		for _, c := range *calls {
			if strings.HasPrefix(c.event, "sweep:") {
				out = append(out, c)
			}
		}
		return out
	}
	withStdin(t, `{"hook_event_name": "Stop", "transcript_path": "/x/a.jsonl"}`, func() {
		Run(t.Context(), "claude", nil, "", ExtensionArgs{})
	})
	if s := sweeps(); len(s) != 0 {
		t.Fatalf("a turn queued a sweep: %v", s)
	}

	// Codex names no transcript for a session it doesn't persist; the sweep still runs.
	withStdin(t, `{"hook_event_name": "SessionStart", "transcript_path": null}`, func() {
		Run(t.Context(), "codex", nil, "", ExtensionArgs{})
	})
	s := sweeps()
	if len(s) != 1 || s[0].event != "sweep:SessionStart" || s[0].agent != "" || s[0].path != "" ||
		!slices.Contains(s[0].args, "--quiet") {
		t.Fatalf("sweeps = %v", s)
	}

	Run(t.Context(), "pi", []string{"/p/s.jsonl"}, "session_shutdown", ExtensionArgs{})
	if s := sweeps(); len(s) != 1 {
		t.Errorf("a second sweep was queued behind the first: %v", s)
	}
	if last := (*calls)[len(*calls)-1]; last.path != "/p/s.jsonl" {
		t.Errorf("session end didn't sync its own transcript: %v", last)
	}

	marker := s[0].args[slices.Index(s[0].args, "--pending-marker")+1]
	if err := os.Remove(marker); err != nil { // as the sweep does once it holds the lock
		t.Fatal(err)
	}
	withStdin(t, `{"hook_event_name": "SessionEnd", "transcript_path": null}`, func() {
		Run(t.Context(), "codex", nil, "", ExtensionArgs{})
	})
	if s := sweeps(); len(s) != 2 || s[1].event != "sweep:SessionEnd" {
		t.Errorf("sweeps after the first started = %v", s)
	}
	if !strings.HasPrefix(marker, filepath.Join(e.State, "pending")) {
		t.Errorf("marker outside the state dir: %s", marker)
	}
}

func TestPiSessionStartPassesReasonAndCwd(t *testing.T) {
	testenv.New(t)
	calls := fakeSpawn(t)
	argv := `["/usr/bin/node", "/usr/lib/pi/dist/cli.js", "--model", "x"]`
	Run(t.Context(), "pi", []string{"/p/s.jsonl"}, "session_start", ExtensionArgs{Source: "resume", Cwd: "/w", Argv: argv})
	if len(*calls) != 2 {
		t.Fatalf("spawned %v", *calls)
	}
	launch := readLaunchFile(t, (*calls)[0].args)
	if launch["source"] != "resume" || launch["cwd"] != "/w" || launch["event"] != "session_start" {
		t.Errorf("launch = %v", launch)
	}
	process := launch["process"].(map[string]any)
	got := process["argv"].([]any)
	if diff := cmp.Diff([]any{"--model", "x"}, got[len(got)-2:]); diff != "" || process["via"] != "extension" {
		t.Errorf("process = %v", process)
	}
}

func TestAgentProcessIsFoundAmongAncestors(t *testing.T) {
	type entry struct {
		argv []string
		ppid int
	}
	tree := map[int]entry{}
	oldProc, oldPPID := readProc, parentPID
	readProc = func(pid int) ([]string, int, bool) {
		e, ok := tree[pid]
		return e.argv, e.ppid, ok
	}
	parentPID = func() int { return 10 }
	t.Cleanup(func() { readProc, parentPID = oldProc, oldPPID })

	tree[10] = entry{[]string{"/bin/sh", "-c", "/opt/agent-sessions hook claude"}, 9}
	tree[9] = entry{[]string{"claude", "--model", "opus"}, 1}
	want := map[string]any{"argv": []string{"claude", "--model", "opus"}, "pid": 9, "ancestry": []string{"sh", "claude"}}
	if diff := cmp.Diff(want, launchProcess("claude")); diff != "" {
		t.Errorf("claude:\n%s", diff)
	}
	clear(tree)
	tree[10] = entry{[]string{"/usr/bin/node", "/usr/lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js", "-c"}, 1}
	if got := launchProcess("pi")["pid"]; got != 10 {
		t.Errorf("pi pid = %v", got)
	}
	clear(tree)
	tree[10] = entry{[]string{"/usr/local/bin/codex exec --full-auto"}, 1}
	if got := launchProcess("codex")["pid"]; got != 10 {
		t.Errorf("codex pid from a whole ps line = %v", got)
	}

	// Run by hand, not by an agent: the walk stops below init and records no command line.
	clear(tree)
	tree[10] = entry{[]string{"/bin/bash", "-l"}, 9}
	tree[9] = entry{nil, 1} // a process that cleared its command line
	tree[1] = entry{[]string{"/sbin/init"}, 0}
	want = map[string]any{"argv": nil, "ancestry": []string{"bash", "?"}}
	if diff := cmp.Diff(want, launchProcess("claude")); diff != "" {
		t.Errorf("no agent:\n%s", diff)
	}
}

func TestProcReadsThisProcess(t *testing.T) {
	argv, ppid, ok := proc(os.Getpid())
	if !ok || len(argv) == 0 || ppid != os.Getppid() {
		t.Errorf("proc(self) = %v, %d, %v", argv, ppid, ok)
	}
	if _, _, ok := proc(-1); ok {
		t.Error("proc found pid -1")
	}
}

// FuzzReadInput feeds arbitrary bytes to the hook's stdin parser, which must never fail.
func FuzzReadInput(f *testing.F) {
	for _, seed := range []string{`{"transcript_path": "/x/a.jsonl"}`, "", "not json", "[1,2]", `{"transcript_path": 3}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "stdin")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		in, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = in.Close() }() // read-only
		got := readInput(in)
		if got == nil {
			t.Fatal("nil input map")
		}
		for _, p := range pathsFromInput(got) {
			if p == "" {
				t.Fatal("empty path")
			}
		}
	})
}
