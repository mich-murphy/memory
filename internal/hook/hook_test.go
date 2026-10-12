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
	withStdin(t, payload, func() { Run(t.Context(), "claude", nil, "", Extra{}) })
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
	// The next turn's Stop arrives while that sync is still queued: nothing new is spawned.
	withStdin(t, `{"transcript_path": "/x/main.jsonl"}`, func() { Run(t.Context(), "claude", nil, "", Extra{}) })
	if len(*calls) != 2 {
		t.Errorf("spawned %d syncs", len(*calls))
	}
}

func TestStalePendingMarkerIsTakenOver(t *testing.T) {
	e := testenv.New(t)
	calls := fakeSpawn(t)
	marker := ClaimPending(e.State, "/x/a.jsonl")
	if marker == "" {
		t.Fatal("no marker")
	}
	testenv.Settle(t, marker, PendingStale+5*time.Second)
	withStdin(t, `{"transcript_path": "/x/a.jsonl"}`, func() { Run(t.Context(), "codex", nil, "", Extra{}) })
	if len(*calls) != 1 || (*calls)[0].path != "/x/a.jsonl" || (*calls)[0].event != "codex" {
		t.Errorf("spawned %v", *calls)
	}
}

func TestHookNeverFails(t *testing.T) {
	for _, payload := range []string{"", "not json", "[1,2]", `{"transcript_path": null}`} {
		t.Run(payload, func(t *testing.T) {
			testenv.New(t)
			calls := fakeSpawn(t)
			withStdin(t, payload, func() { Run(t.Context(), "codex", nil, "", Extra{}) })
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
	Run(t.Context(), "pi", []string{"/x/s.jsonl"}, "", Extra{})
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
	e := testenv.New(t)
	calls := fakeSpawn(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "secret")
	t.Setenv("ANTHROPIC_MODEL", "opus")
	payload := mustJSON(t, map[string]any{
		"hook_event_name": "SessionStart", "source": "startup", "cwd": "/w", "transcript_path": "/x/s.jsonl",
		"permission_mode": "default",
	})
	withStdin(t, payload, func() { Run(t.Context(), "claude", nil, "", Extra{}) })
	if len(*calls) != 1 {
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
	if env["CLAUDE_CODE_ENTRYPOINT"] != "cli" || env["ANTHROPIC_MODEL"] != "opus" {
		t.Errorf("env = %v", env)
	}
	if _, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Error("a token was recorded")
	}
	if len(launch["process"].(map[string]any)["ancestry"].([]any)) == 0 {
		t.Error("no ancestry") // the test's own process tree; no claude in it
	}
	// A session start never takes the debounce marker that turn hooks use.
	markers, _ := filepath.Glob(filepath.Join(e.State, "pending", "[0-9a-f]*"))
	if len(markers) != 0 {
		t.Errorf("markers = %v", markers)
	}
}

func TestPiSessionStartPassesReasonAndCwd(t *testing.T) {
	testenv.New(t)
	calls := fakeSpawn(t)
	argv := `["/usr/bin/node", "/usr/lib/pi/dist/cli.js", "--model", "x"]`
	Run(t.Context(), "pi", []string{"/p/s.jsonl"}, "session_start", Extra{Source: "resume", Cwd: "/w", Argv: argv})
	if len(*calls) != 1 {
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
	readProc = func(_ context.Context, pid int) ([]string, int, bool) {
		e, ok := tree[pid]
		return e.argv, e.ppid, ok
	}
	parentPID = func() int { return 10 }
	t.Cleanup(func() { readProc, parentPID = oldProc, oldPPID })

	tree[10] = entry{[]string{"/bin/sh", "-c", "/opt/agent-sessions hook claude"}, 9}
	tree[9] = entry{[]string{"claude", "--model", "opus"}, 1}
	want := map[string]any{"argv": []string{"claude", "--model", "opus"}, "pid": 9, "ancestry": []string{"sh", "claude"}}
	if diff := cmp.Diff(want, LaunchProcess(t.Context(), "claude")); diff != "" {
		t.Errorf("claude:\n%s", diff)
	}
	clear(tree)
	tree[10] = entry{[]string{"/usr/bin/node", "/usr/lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js", "-c"}, 1}
	if got := LaunchProcess(t.Context(), "pi")["pid"]; got != 10 {
		t.Errorf("pi pid = %v", got)
	}
	// A macOS ps line, kept whole.
	clear(tree)
	tree[10] = entry{[]string{"/usr/local/bin/codex exec --full-auto"}, 1}
	if got := LaunchProcess(t.Context(), "codex")["pid"]; got != 10 {
		t.Errorf("codex pid = %v", got)
	}
}

func TestProcReadsThisProcess(t *testing.T) {
	argv, ppid, ok := proc(t.Context(), os.Getpid())
	if !ok || len(argv) == 0 || ppid != os.Getppid() {
		t.Errorf("proc(self) = %v, %d, %v", argv, ppid, ok)
	}
	// ps is the macOS path; Linux has it too, so both platforms check it. macOS ps may cut the
	// line to the terminal width, so only its presence is checked.
	argv, ppid, ok = procPS(t.Context(), os.Getpid())
	if !ok || len(argv) != 1 || argv[0] == "" || ppid != os.Getppid() {
		t.Errorf("procPS(self) = %q, %d, %v", argv, ppid, ok)
	}
	if _, _, ok := procPS(t.Context(), -1); ok {
		t.Error("procPS found pid -1")
	}
}

func TestStatPPIDCountsFromTheLastParenthesis(t *testing.T) {
	for stat, want := range map[string]int{
		"42 (claude) S 7 42 42 0":     7,
		"42 (a) b (c)) R 9 42 42 0":   9, // a command name holding ") "
		"42 (claude) S":               -1,
		"42 claude S 7 42":            -1,
		"42 (claude) S seven 42 42 0": -1,
	} {
		ppid, ok := statPPID([]byte(stat))
		if !ok {
			ppid = -1
		}
		if ppid != want {
			t.Errorf("statPPID(%q) = %d, %v; want %d", stat, ppid, ok, want)
		}
	}
}

func TestSplitCmdlineDropsEmptyArgsAndRepairsUTF8(t *testing.T) {
	got := splitCmdline([]byte("node\x00/usr/bin/pi\x00\x00--model\x00x\xff\x00"))
	if diff := cmp.Diff([]string{"node", "/usr/bin/pi", "--model", "x\uFFFD"}, got); diff != "" {
		t.Errorf("splitCmdline (-want +got):\n%s", diff)
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
		got := ReadInput(in)
		if got == nil {
			t.Fatal("nil input map")
		}
		for _, p := range PathsFromInput(got) {
			if p == "" {
				t.Fatal("empty path")
			}
		}
	})
}
