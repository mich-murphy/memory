package install

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func opts(e *testenv.Env) *Options {
	return &Options{Command: testCmd, Home: e.Home}
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

	if err := Claude(opts(e)); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Claude(opts(e)); err != nil {
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

	remove := opts(e)
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
	if err := Claude(opts(e)); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, path)["cleanupPeriodDays"]; got != 9999.0 {
		t.Errorf("cleanupPeriodDays = %v", got)
	}
	testenv.Write(t, path, "{ not json")
	if err := Claude(opts(e)); err == nil {
		t.Error("installed over a broken settings.json")
	}
	if data, _ := os.ReadFile(path); string(data) != "{ not json" {
		t.Errorf("broken settings.json rewritten: %q", data)
	}
}

func TestClaudeHookCommandQuotesPathsWithSpaces(t *testing.T) {
	e := testenv.New(t)
	o := opts(e)
	o.Command = []string{"/Users/me/My Tools/agent-sessions"}
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
	if err := Claude(opts(e)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"a && b < c > d"`) {
		t.Errorf("command escaped:\n%s", data)
	}
}

func TestCodexInstallWritesHooksJSONAndReportsTrust(t *testing.T) {
	e := testenv.New(t)
	root := filepath.Join(e.Home, ".codex")
	o := opts(e)
	if err := Codex(o); err != nil {
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
	if !slices.ContainsFunc(o.Log, func(l string) bool { return strings.Contains(l, "run /hooks") }) {
		t.Errorf("no trust hint in %v", o.Log)
	}
	if n, trusted := CodexTrustState(root); n != 6 || trusted != 0 {
		t.Errorf("trust = %d/%d", trusted, n)
	}

	trust := func(labels ...string) {
		t.Helper()
		var toml strings.Builder
		for _, l := range labels {
			fmt.Fprintf(&toml, "\n[hooks.state.%q]\ntrusted_hash = \"sha256:x\"\n", hooksJSON+":"+l+":0:0")
		}
		testenv.Append(t, filepath.Join(root, "config.toml"), toml.String())
	}
	trust("stop", "session_end")
	if n, trusted := CodexTrustState(root); n != 6 || trusted != 2 {
		t.Errorf("trust after two = %d/%d", trusted, n)
	}
	trust("session_start", "subagent_stop", "pre_compact", "interrupt")
	if n, trusted := CodexTrustState(root); n != 6 || trusted != 6 {
		t.Errorf("trust after all = %d/%d", trusted, n)
	}

	dryRun := opts(e)
	dryRun.Remove, dryRun.DryRun = true, true
	if err := Codex(dryRun); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hooksJSON); err != nil {
		t.Errorf("a dry-run uninstall removed hooks.json: %v", err)
	}
	remove := opts(e)
	remove.Remove = true
	if err := Codex(remove); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hooksJSON); !os.IsNotExist(err) {
		t.Error("hooks.json left behind")
	}
}

func TestCodexUninstallKeepsOtherHooks(t *testing.T) {
	e := testenv.New(t)
	hooksJSON := filepath.Join(e.Home, ".codex", "hooks.json")
	testenv.Write(t, hooksJSON, `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]}]}}`)
	theirs := readJSON(t, hooksJSON)
	if err := Codex(opts(e)); err != nil {
		t.Fatal(err)
	}
	remove := opts(e)
	remove.Remove = true
	if err := Codex(remove); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(theirs, readJSON(t, hooksJSON)); diff != "" {
		t.Errorf("hooks.json after uninstall (-before +after):\n%s", diff)
	}
}

func TestInstallWarnsOnceWhenHooksRunABinaryOffPath(t *testing.T) {
	e := testenv.New(t)
	dir := t.TempDir()
	onPath := filepath.Join(dir, "agent-sessions")
	testenv.Write(t, onPath, "#!/bin/sh\n")
	if err := os.Chmod(onPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	warnings := func(command string) int {
		o := &Options{Command: []string{command}, Home: e.Home}
		for _, install := range []func(*Options) error{Claude, Codex, Pi} {
			if err := install(o); err != nil {
				t.Fatal(err)
			}
		}
		return len(slices.DeleteFunc(o.Log, func(l string) bool { return !strings.HasPrefix(l, "warning: the hooks run "+command) }))
	}
	if n := warnings(onPath); n != 0 {
		t.Errorf("%d warnings for the agent-sessions on PATH", n)
	}
	if n := warnings("/home/me/Downloads/agent-sessions"); n != 1 {
		t.Errorf("%d warnings for a binary off PATH, want 1", n)
	}
}

func TestUninstallWithoutTheAgentsChangesAndReportsNothing(t *testing.T) {
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(env, "")
	}
	home := t.TempDir()
	o := &Options{Command: testCmd, Home: home, Remove: true}
	for _, uninstall := range []func(*Options) error{Claude, Codex, Pi} {
		if err := uninstall(o); err != nil {
			t.Fatal(err)
		}
	}
	if len(o.Log) > 0 {
		t.Errorf("log = %q, want nothing", o.Log)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) > 0 {
		t.Errorf("home after uninstall = %v, %v; want it empty", entries, err)
	}
}

// Codex's trust keys hold each handler's position, so a reinstall must not move any hook.
func TestCodexReinstallKeepsEveryHookInPlace(t *testing.T) {
	e := testenv.New(t)
	hooksJSON := filepath.Join(e.Home, ".codex", "hooks.json")
	if err := Codex(opts(e)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hooksJSON)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonx.ParseObject(data)
	if err != nil {
		t.Fatal(err)
	}
	hooks, _ := doc.Get("hooks")
	stop, _ := hooks.(*jsonx.Object).Get("Stop")
	theirs := jsonx.NewObject()
	theirs.Set("type", "command")
	theirs.Set("command", "notify-send done")
	group := jsonx.NewObject()
	group.Set("hooks", []any{theirs})
	hooks.(*jsonx.Object).Set("Stop", append(stop.([]any), group))
	before, err := jsonx.Indent(doc, 2)
	if err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, hooksJSON, string(before))

	o := opts(e)
	if err := Codex(o); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(hooksJSON); !bytes.Equal(after, before) {
		t.Errorf("an unchanged reinstall rewrote hooks.json:\n%s", cmp.Diff(string(before), string(after)))
	}
	if !slices.Contains(o.Log, "unchanged: "+hooksJSON) {
		t.Errorf("log = %v", o.Log)
	}

	o = opts(e)
	o.Command = []string{"/new/agent-sessions"}
	if err := Codex(o); err != nil {
		t.Fatal(err)
	}
	var commands []any
	for _, g := range readJSON(t, hooksJSON)["hooks"].(map[string]any)["Stop"].([]any) {
		commands = append(commands, g.(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"])
	}
	if diff := cmp.Diff([]any{"/new/agent-sessions hook codex", "notify-send done"}, commands); diff != "" {
		t.Errorf("Stop commands after a new install path (-want +got):\n%s", diff)
	}
}

func TestPiInstallWritesExtensionAndRespectsForeignFile(t *testing.T) {
	e := testenv.New(t)
	if err := Pi(opts(e)); err != nil {
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
	if err := Pi(opts(e)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(ext); string(data) != "// someone else's extension\n" {
		t.Error("overwrote a foreign extension")
	}
}
