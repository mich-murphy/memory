package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/testenv"
)

var testCmd = []string{"/opt/agent-sessions/bin/agent-sessions"}

func opts(e *testenv.Env, log *[]string) *Options {
	return &Options{Command: testCmd, Home: e.Home, KeepDays: 3650, Log: log}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func firstHandler(t *testing.T, cfg map[string]any, event string) map[string]any {
	t.Helper()
	groups := cfg["hooks"].(map[string]any)[event].([]any)
	return groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
}

func TestClaudeInstallMergesIdempotentlyAndUninstalls(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.Claude, "settings.json")
	settings := readJSON(t, path)
	settings["hooks"] = map[string]any{
		"Stop": []any{
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "notify-send done"}}},
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/old/bin/agent-sessions hook claude"}}},
		},
		"PreToolUse": []any{map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "guard.sh"}}}},
	}
	settings["cleanupPeriodDays"] = 60
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, path, string(body))

	var log []string
	if err := Claude(opts(e, &log)); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Claude(opts(e, &log)); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(again, first) {
		t.Errorf("second install changed settings.json:\n%s", cmp.Diff(string(first), string(again)))
	}
	got := readJSON(t, path)
	if got["cleanupPeriodDays"] != 3650.0 {
		t.Errorf("cleanupPeriodDays = %v", got["cleanupPeriodDays"])
	}
	if diff := cmp.Diff(settings["env"], got["env"]); diff != "" {
		t.Errorf("env changed:\n%s", diff)
	}
	stop := got["hooks"].(map[string]any)["Stop"].([]any)
	if c := stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"]; c != "notify-send done" {
		t.Errorf("first Stop handler = %v", c)
	}
	want := map[string]any{"type": "command", "command": "/opt/agent-sessions/bin/agent-sessions hook claude", "async": true, "timeout": 30.0}
	if diff := cmp.Diff(want, stop[1].(map[string]any)["hooks"].([]any)[0]); diff != "" {
		t.Errorf("our Stop handler:\n%s", diff)
	}
	var events []string
	for k := range got["hooks"].(map[string]any) {
		events = append(events, k)
	}
	slices.Sort(events)
	wantEvents := []string{"PreCompact", "PreToolUse", "SessionEnd", "SessionStart", "Stop", "StopFailure", "SubagentStop", "TaskCompleted", "TeammateIdle"}
	if diff := cmp.Diff(wantEvents, events); diff != "" {
		t.Errorf("events:\n%s", diff)
	}
	if _, ok := firstHandler(t, got, "SessionEnd")["async"]; ok {
		t.Error("SessionEnd is async")
	}
	if backups, _ := filepath.Glob(path + ".agent-sessions-backup-*"); len(backups) == 0 {
		t.Error("no backup written")
	}
	in, err := jsonx.ParseObject(body)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonx.ParseObject(first)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(in.Keys(), out.Keys()); diff != "" {
		t.Errorf("settings.json keys reordered:\n%s", diff)
	}

	remove := opts(e, &log)
	remove.Remove = true
	if err := Claude(remove); err != nil {
		t.Fatal(err)
	}
	after := readJSON(t, path)["hooks"].(map[string]any)
	wantAfter := map[string]any{} // the entry from an earlier install path is gone too
	for k, v := range settings["hooks"].(map[string]any) {
		wantAfter[k] = v.([]any)[:1]
	}
	if diff := cmp.Diff(wantAfter, after); diff != "" {
		t.Errorf("after uninstall:\n%s", diff)
	}
}

func TestClaudeInstallNeverLowersRetentionAndRefusesBrokenJSON(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.Claude, "settings.json")
	testenv.Write(t, path, `{"cleanupPeriodDays": 9999}`)
	var log []string
	if err := Claude(opts(e, &log)); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, path)["cleanupPeriodDays"]; got != 9999.0 {
		t.Errorf("cleanupPeriodDays = %v", got)
	}
	testenv.Write(t, path, "{ not json")
	if err := Claude(opts(e, &log)); err == nil {
		t.Error("installed over a broken settings.json")
	}
	if data, _ := os.ReadFile(path); string(data) != "{ not json" {
		t.Errorf("broken settings.json rewritten: %q", data)
	}
}

func TestClaudeHookCommandQuotesPathsWithSpaces(t *testing.T) {
	e := testenv.New(t)
	var log []string
	o := opts(e, &log)
	o.Command = []string{"/Users/me/My Tools/agent-sessions"}
	o.KeepDays = 0
	if err := Claude(o); err != nil {
		t.Fatal(err)
	}
	cmd := firstHandler(t, readJSON(t, filepath.Join(e.Claude, "settings.json")), "Stop")["command"]
	if cmd != "'/Users/me/My Tools/agent-sessions' hook claude" {
		t.Errorf("command = %v", cmd)
	}
}

func TestClaudeSettingsKeepShellOperatorsUnescaped(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.Claude, "settings.json")
	testenv.Write(t, path, `{"statusLine": {"command": "a && b < c > d"}}`)
	var log []string
	if err := Claude(opts(e, &log)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"a && b < c > d"`) {
		t.Errorf("command escaped:\n%s", data)
	}
}

func TestCodexInstallWritesHooksJSONAndReportsTrust(t *testing.T) {
	e := testenv.New(t)
	root := filepath.Join(e.Home, ".codex")
	var log []string
	if err := Codex(opts(e, &log)); err != nil {
		t.Fatal(err)
	}
	hooksJSON := filepath.Join(root, "hooks.json")
	cfg := readJSON(t, hooksJSON)
	var events []string
	for k := range cfg["hooks"].(map[string]any) {
		events = append(events, k)
	}
	slices.Sort(events)
	if diff := cmp.Diff([]string{"Interrupt", "PreCompact", "SessionEnd", "SessionStart", "Stop", "SubagentStop"}, events); diff != "" {
		t.Errorf("events:\n%s", diff)
	}
	want := map[string]any{"type": "command", "command": "/opt/agent-sessions/bin/agent-sessions hook codex", "timeout": 3.0}
	if diff := cmp.Diff(want, firstHandler(t, cfg, "SessionEnd")); diff != "" {
		t.Errorf("SessionEnd handler:\n%s", diff)
	}
	if !slices.ContainsFunc(log, func(l string) bool { return strings.Contains(l, "/hooks") }) {
		t.Errorf("no trust hint in %v", log)
	}
	if n, trusted := CodexTrustState(root); n != 6 || trusted != 0 {
		t.Errorf("trust = %d/%d", trusted, n)
	}

	// Simulate the user trusting two of them in /hooks.
	testenv.Append(t, filepath.Join(root, "config.toml"),
		"\n[hooks.state.\""+hooksJSON+":stop:0:0\"]\ntrusted_hash = \"sha256:x\"\n"+
			"\n[hooks.state.\""+hooksJSON+":session_end:0:0\"]\ntrusted_hash = \"sha256:y\"\n")
	if n, trusted := CodexTrustState(root); n != 6 || trusted != 2 {
		t.Errorf("trust = %d/%d", trusted, n)
	}

	remove := opts(e, &log)
	remove.Remove = true
	if err := Codex(remove); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hooksJSON); !os.IsNotExist(err) {
		t.Error("hooks.json left behind")
	}
}

func TestPiInstallWritesExtensionAndRespectsForeignFile(t *testing.T) {
	e := testenv.New(t)
	var log []string
	if err := Pi(opts(e, &log)); err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(e.Home, ".pi", "agent", "extensions", "agent-sessions.ts")
	data, err := os.ReadFile(ext)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{PiMarker, `["/opt/agent-sessions/bin/agent-sessions"]`, `pi.on("agent_settled"`, `["--event", "session_shutdown"]`, `"--event", "session_start"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("extension lacks %q", want)
		}
	}
	testenv.Write(t, ext, "// someone else's extension\n")
	if err := Pi(opts(e, &log)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(ext); string(data) != "// someone else's extension\n" {
		t.Error("overwrote a foreign extension")
	}
}

func TestCodexEventKey(t *testing.T) {
	for in, want := range map[string]string{"Stop": "stop", "SessionEnd": "session_end", "PreCompact": "pre_compact"} {
		if got := codexEventKey(in); got != want {
			t.Errorf("codexEventKey(%q) = %q", in, got)
		}
	}
}
