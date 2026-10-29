package capturehooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/shellwords"
)

func sandbox(t *testing.T) Options {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "agent sessions '$% ü")
	config := filepath.Join(home, "capture '$% ü.json")
	put(t, exe, "#!/bin/sh\nexit 0\n", 0o700)
	put(t, config, `{"private":"CONFIGSECRET"}`, 0o600)
	return Options{Home: home, Executable: exe, ConfigPath: config}
}

func put(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func build(t *testing.T, o Options) Plan {
	t.Helper()
	p, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func apply(t *testing.T, p Plan) {
	t.Helper()
	if err := Apply(t.Context(), p, true); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tree(t *testing.T, home string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(home, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			m[p] = string(get(t, p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSandboxLifecycle(t *testing.T) {
	o := sandbox(t)
	claude := filepath.Join(o.Home, ".claude", "settings.json")
	codex := filepath.Join(o.Home, ".codex", "hooks.json")
	legacy := `{"type":"command","command":"agent-sessions hook claude"}`
	settings := `{"secret":"SETTINGSECRET","cleanupPeriodDays":7,"large":9007199254740993,"hooks":{"Stop":[{"matcher":"foreign","unknown":true,"hooks":[` + legacy + `,{"type":"command","command":"foreign"}]}]}}`
	// Synthetic existing native configuration is cloned into a fake home only.
	put(t, claude, settings, 0o644)
	put(t, codex, `{"hooks":{},"unknown":{"token":"CODEXSECRET"}}`, 0o600)
	put(t, filepath.Join(o.Home, ".codex", "config.toml"), "[hooks.state]\n# keep trust\n", 0o600)
	put(t, filepath.Join(o.Home, ".pi", "agent", "extensions", "agent-sessions.ts"), "// legacy extension", 0o600)
	initial := tree(t, o.Home)
	p := build(t, o)
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SETTINGSECRET", "CODEXSECRET", "CONFIGSECRET", "cleanupPeriodDays", "legacy extension"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("plan leaks %s", secret)
		}
	}
	if err := Apply(t.Context(), p, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial, tree(t, o.Home)) {
		t.Fatal("planning/dry run changed filesystem")
	}
	apply(t, p)
	p2 := build(t, o)
	for _, c := range p2.Changes {
		if c.Changed {
			t.Fatalf("not idempotent: %s", c.Path)
		}
	}
	installed := tree(t, o.Home)
	apply(t, p2)
	if !reflect.DeepEqual(installed, tree(t, o.Home)) {
		t.Fatal("idempotent apply wrote backups")
	}
	for _, c := range p.Changes {
		st, err := os.Stat(c.Path)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("not private: %v %v", st, err)
		}
		if c.Agent == "pi" {
			continue
		}
		doc, err := jsonx.ParseObject(get(t, c.Path))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := doc.Get("hooks")
		hooks := raw.(*jsonx.Object)
		for _, ev := range events(c.Agent) {
			raw, _ := hooks.Get(ev.name)
			groups := raw.([]any)
			found := false
			for _, g := range groups {
				raw, _ := g.(*jsonx.Object).Get("hooks")
				for _, h := range raw.([]any) {
					raw, _ := h.(*jsonx.Object).Get("command")
					cmd, _ := raw.(string)
					if suffix, ok := strings.CutPrefix(cmd, commandPrefix); ok {
						argv, err := shellwords.Split(suffix)
						if err != nil || !reflect.DeepEqual(argv, c.Command) {
							t.Fatalf("bad argv %q: %v", argv, err)
						}
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("missing %s", ev.name)
			}
		}
	}
	body := string(get(t, claude))
	for _, want := range []string{"SETTINGSECRET", "9007199254740993", "agent-sessions hook claude", "foreign", "\"cleanupPeriodDays\":7"} {
		if !strings.Contains(body, want) {
			t.Fatalf("lost %s", want)
		}
	}
	o.Remove = true
	apply(t, build(t, o))
	p3 := build(t, o)
	for _, c := range p3.Changes {
		if c.Changed {
			t.Fatal("uninstall not idempotent")
		}
	}
	for path, body := range initial {
		if path == claude || path == codex {
			continue
		}
		if string(get(t, path)) != body {
			t.Fatalf("changed preserved source: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(o.Home, ".pi", "agent", "extensions", Marker+".ts")); !os.IsNotExist(err) {
		t.Fatal("extension not removed")
	}
	matches, err := filepath.Glob(claude + ".agent-sessions-backup-*")
	if err != nil || len(matches) != 2 {
		t.Fatalf("backups %v %v", matches, err)
	}
	if string(get(t, matches[0])) != settings && string(get(t, matches[1])) != settings {
		t.Fatal("original backup missing")
	}
	for _, path := range matches {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatal("unsafe backup")
		}
	}
}

func TestRefusals(t *testing.T) {
	for _, body := range []string{"", `{"secret":"SECRET",`, `[]`, `{"hooks":null}`, `{"hooks":{"Stop":{}}}`, `{"hooks":{"Stop":[null]}}`, `{"hooks":{"Stop":[{}]}}`, `{"hooks":{"Stop":[{"hooks":null}]}}`, `{"hooks":{"Stop":[{"hooks":[null]}]}}`, `{"x":1,"x":2}`} {
		t.Run(body, func(t *testing.T) {
			o := sandbox(t)
			path := filepath.Join(o.Home, ".claude", "settings.json")
			put(t, path, body, 0o600)
			before := tree(t, o.Home)
			_, err := Build(o)
			if err == nil {
				t.Fatal("accepted malformed config")
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatal("diagnostic leak")
			}
			if !reflect.DeepEqual(before, tree(t, o.Home)) {
				t.Fatal("changed malformed settings")
			}
		})
	}
	t.Run("concurrent edit", func(t *testing.T) {
		o := sandbox(t)
		p := build(t, o)
		path := p.Changes[0].Path
		put(t, path, `{"concurrent":true}`, 0o600)
		before := tree(t, o.Home)
		if Apply(t.Context(), p, true) == nil {
			t.Fatal("clobbered concurrent edit")
		}
		if !reflect.DeepEqual(before, tree(t, o.Home)) {
			t.Fatal("partial writes before preflight")
		}
	})
	t.Run("symlink ancestor", func(t *testing.T) {
		o := sandbox(t)
		if err := os.Symlink(o.Home, filepath.Join(o.Home, ".claude")); err != nil {
			t.Fatal(err)
		}
		if _, err := Build(o); err == nil {
			t.Fatal("accepted symlink")
		}
	})
	t.Run("symlink after plan", func(t *testing.T) {
		o := sandbox(t)
		p := build(t, o)
		if err := os.Symlink(o.ConfigPath, p.Changes[0].Path); err == nil {
			t.Fatal("unexpected parent exists")
		}
		if err := os.MkdirAll(filepath.Dir(p.Changes[0].Path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(o.ConfigPath, p.Changes[0].Path); err != nil {
			t.Fatal(err)
		}
		if Apply(t.Context(), p, true) == nil {
			t.Fatal("accepted replacement symlink")
		}
	})
	t.Run("foreign candidate", func(t *testing.T) {
		o := sandbox(t)
		path := filepath.Join(o.Home, ".pi", "agent", "extensions", Marker+".ts")
		put(t, path, "// foreign "+Marker, 0o600)
		for _, remove := range []bool{false, true} {
			o.Remove = remove
			if _, err := Build(o); err == nil {
				t.Fatal("accepted foreign extension")
			}
		}
	})
	t.Run("serialized plan", func(t *testing.T) {
		if Apply(t.Context(), Plan{}, true) == nil {
			t.Fatal("accepted fabricated plan")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		o := sandbox(t)
		p := build(t, o)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if Apply(ctx, p, true) == nil {
			t.Fatal("ignored cancellation")
		}
	})
}

func TestExplicitRootsAndPrivateInputs(t *testing.T) {
	o := sandbox(t)
	o.Roots = []Root{{Agent: "codex", Path: filepath.Join(o.Home, "learned root")}, {Agent: "codex", Path: filepath.Join(o.Home, "learned root")}}
	p := build(t, o)
	if len(p.Changes) != 1 || !strings.Contains(p.Changes[0].Path, "learned root") {
		t.Fatal("wrong inventory")
	}
	for _, mutate := range []func(*Options){func(o *Options) { o.Home = "relative" }, func(o *Options) { o.ConfigPath = "relative" }, func(o *Options) { o.Executable = "relative" }, func(o *Options) { o.Roots = []Root{{Agent: "unknown", Path: o.Home}} }, func(o *Options) { o.Roots = []Root{{Agent: "codex", Path: "relative"}} }, func(o *Options) {
		if err := os.Chmod(o.ConfigPath, 0o644); err != nil {
			t.Fatal(err)
		}
	}, func(o *Options) {
		if err := os.Chmod(o.Executable, 0o600); err != nil {
			t.Fatal(err)
		}
	}} {
		fresh := sandbox(t)
		mutate(&fresh)
		if _, err := Build(fresh); err == nil {
			t.Fatal("accepted unsafe options")
		}
	}
}

func TestQuotingAndPiStaticSDK(t *testing.T) {
	for _, s := range []string{"apostrophe'", `double"quote`, "spaces here", "%s", "$HOME;$(false)", "line\nnext", "UTF8 ü雪"} {
		argv := []string{"/tmp/" + s, "capture-v1", "hook", "--config", "/tmp/config " + s, "--agent", "claude"}
		got, err := shellwords.Split(shellwords.Join(argv))
		if err != nil || !reflect.DeepEqual(got, argv) {
			t.Fatalf("roundtrip %q", s)
		}
		b, err := piExtension(argv)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{piPrefix, `import type { ExtensionAPI }`, `pi.on("session_start"`, `pi.on("agent_settled"`, `pi.on("session_shutdown"`, `"--path", file`, `stdio: "ignore"`, `timeout: 2500`, `killSignal: "SIGKILL"`} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("missing %s", want)
			}
		}
		for _, bad := range []string{"child.stdout", "child.stderr", "readFile", "console.", "detached:", "hook"} {
			if bad == "hook" {
				continue
			}
			if strings.Contains(string(b), bad) {
				t.Fatalf("unsafe Pi source: %s", bad)
			}
		}
	}
	o := sandbox(t)
	o.ConfigPath = o.Home + "/bad\nconfig"
	if _, err := Build(o); err == nil {
		t.Fatal("SafePath newline must fail")
	}
}

func TestPlanBudgets(t *testing.T) {
	o := sandbox(t)
	for range maxRoots + 1 {
		o.Roots = append(o.Roots, Root{Agent: "codex", Path: o.Home})
	}
	if _, err := Build(o); err == nil {
		t.Fatal("accepted excessive roots")
	}
	o.Roots = nil
	body := `{"padding":"` + strings.Repeat("x", 3<<20) + `"}`
	for i := range 4 {
		root := filepath.Join(o.Home, string(rune('a'+i)))
		o.Roots = append(o.Roots, Root{Agent: "codex", Path: root})
		put(t, filepath.Join(root, "hooks.json"), body, 0o600)
	}
	before := tree(t, o.Home)
	if _, err := Build(o); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("missing byte backpressure: %v", err)
	}
	if !reflect.DeepEqual(before, tree(t, o.Home)) {
		t.Fatal("budget rejection wrote files")
	}
}

// Execute only a synthetic executable in a fake home, never a native agent.
func TestNativeShellCommandPreservesArgumentsAndStdin(t *testing.T) {
	o := sandbox(t)
	put(t, o.Executable, "#!/bin/sh\nprintf '%s\\000' \"$@\"\n/bin/cat\n", 0o700)
	p := build(t, o)
	for _, fact := range p.Changes {
		if fact.Agent == "pi" {
			continue
		}
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", commandPrefix+shellwords.Join(fact.Command))
		cmd.Stdin = strings.NewReader("synthetic stdin metadata\n")
		got, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Join(fact.Command[1:], "\x00") + "\x00synthetic stdin metadata\n"
		if string(got) != want {
			t.Fatalf("argv/stdin changed: %q", got)
		}
	}
}

func TestApplyRevalidatesProtectedInputsAndIgnoresExportedFacts(t *testing.T) {
	t.Run("config permissions", func(t *testing.T) {
		o := sandbox(t)
		p := build(t, o)
		if err := os.Chmod(o.ConfigPath, 0o644); err != nil {
			t.Fatal(err)
		}
		before := tree(t, o.Home)
		if Apply(t.Context(), p, true) == nil {
			t.Fatal("accepted public config")
		}
		if !reflect.DeepEqual(before, tree(t, o.Home)) {
			t.Fatal("wrote before config check")
		}
	})
	t.Run("executable symlink", func(t *testing.T) {
		o := sandbox(t)
		p := build(t, o)
		if err := os.Remove(o.Executable); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(o.ConfigPath, o.Executable); err != nil {
			t.Fatal(err)
		}
		if Apply(t.Context(), p, true) == nil {
			t.Fatal("accepted symlink executable")
		}
	})
	t.Run("exported facts not mutation authority", func(t *testing.T) {
		o := sandbox(t)
		p := build(t, o)
		foreign := filepath.Join(o.Home, "unrelated")
		put(t, foreign, "FOREIGN", 0o600)
		p.Changes[0].Path = foreign
		p.Changes[0].Delete = true
		p.Changes[0].BeforeSHA256 = "invented"
		apply(t, p)
		if string(get(t, foreign)) != "FOREIGN" {
			t.Fatal("trusted exported facts")
		}
	})
}

func TestRemovalWithoutOldExecutable(t *testing.T) {
	o := sandbox(t)
	apply(t, build(t, o))
	if err := os.Remove(o.Executable); err != nil {
		t.Fatal(err)
	}
	o.Remove = true
	apply(t, build(t, o))
	o.Remove = false
	if _, err := Build(o); err == nil {
		t.Fatal("activation accepted missing executable")
	}
}
