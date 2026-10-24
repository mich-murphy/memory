package main

// Actual native acceptance is separate from AGENT_SESSIONS_LIVE. All agent,
// hook and coordinator processes inherit the existing loopback-only sandbox.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/shellwords"
)

// Observe without changing native input or manufacturing agent events. Decode
// one object: Codex deliberately does not close its hook pipe until hook exit.
func init() {
	root := os.Getenv("MEMORY_CAPTURE_NATIVE_OBSERVER")
	if root == "" {
		return
	}
	if err := captureNativeObserve(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func captureNativeObserve(root string) error {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return errors.New("missing observer separator")
	}
	args := os.Args[separator+1:]
	f, err := os.CreateTemp(filepath.Join(root, "events"), "native.*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(name+".argv", []byte(strings.Join(args, "\n")), 0o600); err != nil {
		return err
	}
	var payload json.RawMessage
	if !slices.Contains(args, "--path") {
		if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
			return err
		}
	}
	if err := os.WriteFile(name+".input", payload, 0o600); err != nil {
		return err
	}
	// Exec preserves the actual native agent ancestry used by launch inspection.
	input, err := os.Open(name + ".input")
	if err != nil {
		return err
	}
	// syscall.Dup2 is unavailable on Darwin; use the already shared observer's
	// stdin replacement through /dev/fd via the unix implementation below.
	return captureNativeExec(input, args)
}

func captureNativeExec(input *os.File, args []string) error {
	if err := unix.Dup2(int(input.Fd()), 0); err != nil {
		_ = input.Close()
		return err
	}
	_ = input.Close() // stdin owns the read-only descriptor.
	binary := os.Getenv("MEMORY_CAPTURE_NATIVE_BINARY")
	return syscall.Exec(binary, append([]string{binary}, args...), os.Environ())
}

func TestCaptureNativeLive(t *testing.T) {
	if os.Getenv("AGENT_SESSIONS_CAPTURE_NATIVE") != "1" {
		t.Skip("explicit scratch-only native acceptance: AGENT_SESSIONS_CAPTURE_NATIVE=1")
	}
	if !filepath.IsAbs(os.Getenv("CANDIDATE_NATIVE_ANALYTICS_PYTHON")) {
		t.Fatal("live acceptance requires explicit absolute CANDIDATE_NATIVE_ANALYTICS_PYTHON; no dependency installation")
	}
	if runtime.GOOS != "darwin" {
		t.Fatal("requires macOS sandbox-exec; never run unsandboxed")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex", "pi"} {
		t.Run(agent, func(t *testing.T) { captureNativeAgent(t, agent) })
	}
}

func captureNativeAgent(t *testing.T, agent string) {
	t.Helper()
	model := &liveModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	l := newLiveRun(t, port)
	l.verifyIsolation(t, port)
	config := filepath.Join(l.root, "candidate.json")
	binary := filepath.Join(binDir, "agent-sessions")
	observer := filepath.Join(l.root, "bin", "agent-sessions")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	liveWrite(t, observer, "#!/bin/sh\nexport MEMORY_CAPTURE_NATIVE_OBSERVER="+shellwords.Join([]string{l.root})+"\nexport MEMORY_CAPTURE_NATIVE_BINARY="+shellwords.Join([]string{binary})+"\nexec "+shellwords.Join([]string{executable})+" -test.run=^TestCaptureNativeObserverProcess$ -- \"$@\"\n", 0o700)
	c := capturedaemon.Config{Version: capturedaemon.ConfigVersion, ArchiveID: "native-acceptance", MachineID: "scratch-mac", Store: filepath.Join(l.root, "bucket"), Spool: filepath.Join(l.root, "queue"), PolicyDir: filepath.Join(l.root, "policy"), Home: filepath.Join(l.root, "home"), Roots: []capturedaemon.Root{{Agent: agent, Path: filepath.Join(l.root, agent)}}, Enrichment: true, EnrichmentRoots: []string{l.work}, PollSeconds: 1, OperationSeconds: 10, CaptureWorkers: 2, DeliveryWorkers: 2, MaxAttempts: 3, MaxSpoolBytes: 64 << 20, EntriesPerRoot: 256}
	if err := os.MkdirAll(filepath.Join(c.PolicyDir, "exclusions"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	liveWrite(t, config, string(data), 0o600)
	l.run(t, binary, "capture-v1", "init", "--config", config)
	l.run(t, binary, "capture-v1", "hooks", "--config", config, "--executable", observer, "--action", "install", "--apply")
	t.Logf("actual %s version: %s", agent, strings.TrimSpace(string(l.run(t, "/opt/homebrew/bin/"+agent, "--version"))))
	args := captureNativeArgs(t, l, agent, server.URL, config, observer)
	if agent == "codex" {
		path := filepath.Join(l.root, "codex", "config.toml")
		approved, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(approved), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "trusted_hash = ") {
				lines[i] = `trusted_hash = "sha256:` + strings.Repeat("0", 64) + `"`
			}
		}
		liveWrite(t, path, strings.Join(lines, "\n"), 0o600)
		captureNativeNoHooks(t, l, agent, args, "wrong-trusted-hash")
		liveWrite(t, path, string(approved), 0o600)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 85*time.Second)
	defer cancel()
	coordinator := l.command(ctx, binary, "capture-v1", "run", "--config", config)
	log, err := os.OpenFile(filepath.Join(l.root, "coordinator.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Stdout, coordinator.Stderr = log, log
	if err := coordinator.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = coordinator.Process.Signal(syscall.SIGTERM) // Process may already have exited.
		if err := coordinator.Wait(); err != nil && ctx.Err() == nil {
			t.Errorf("coordinator: %v (scratch log retained on failure)", err)
		}
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	})
	var out []byte
	if agent == "pi" {
		out = l.run(t, "/opt/homebrew/bin/pi", args...)
	} else {
		out = l.runHeld(t, agent, "/opt/homebrew/bin/"+agent, args)
	}
	if !bytes.Contains(out, []byte(liveReply)) {
		t.Fatal("actual agent did not return loopback reply")
	}
	model.mu.Lock()
	requests, unexpected := slices.Clone(model.requests), slices.Clone(model.unexpected)
	model.mu.Unlock()
	if len(requests) == 0 || len(unexpected) != 0 {
		t.Fatalf("loopback requests=%v unexpected=%v", requests, unexpected)
	}
	var accepted bool
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if captureNativeRead(t, l, agent) {
			accepted = true
			break
		}
	}
	if !accepted {
		t.Fatalf("native fields or byte-exact candidate publication missing; scratch=%s", l.root)
	}
	captureNativeEvents(t, l, agent)
	// Removal is applied only to scratch. Subsequent native session proves disabled
	// hook behavior (periodic discovery remains allowed, and is not hook evidence).
	l.run(t, binary, "capture-v1", "hooks", "--config", config, "--executable", observer, "--action", "uninstall", "--apply")
	captureNativeNoHooks(t, l, agent, args, "uninstalled")
}

func captureNativeArgs(t *testing.T, l liveRun, agent, baseURL, config, observer string) []string {
	t.Helper()
	switch agent {
	case "claude":
		b, err := json.Marshal(map[string]any{"hasCompletedOnboarding": true, "theme": "dark", "customApiKeyResponses": map[string]any{"approved": []string{"hetic-acceptance-key"}, "rejected": []string{}}, "projects": map[string]any{l.work: map[string]any{"hasTrustDialogAccepted": true}}})
		if err != nil {
			t.Fatal(err)
		}
		liveWrite(t, filepath.Join(l.root, "claude", ".claude.json"), string(b), 0o600)
		return []string{"--debug-file", filepath.Join(l.root, "claude-debug.log"), "--permission-mode", "default", "--model", "claude-sonnet-4-6", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--", livePrompt}
	case "codex":
		captureNativeTrustCodex(t, l, baseURL, config, observer)
		return []string{"--no-alt-screen", "--sandbox", "read-only", "--disable", "shell_tool", "-c", `web_search="disabled"`}
	case "pi":
		liveWrite(t, filepath.Join(l.root, "pi", "models.json"), fmt.Sprintf(`{"providers":{"acceptance":{"baseUrl":%q,"api":"anthropic-messages","apiKey":"synthetic-acceptance-key","models":[{"id":"acceptance","name":"Acceptance","reasoning":false,"input":["text"],"contextWindow":32000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, baseURL), 0o600)
		return []string{"--print", "--offline", "--provider", "acceptance", "--model", "acceptance", "--thinking", "off", "--no-tools", "--no-mcp", "--no-skills", "--no-prompt-templates", "--no-themes", livePrompt}
	}
	t.Fatal("unsupported agent")
	return nil
}

func captureNativeTrustCodex(t *testing.T, l liveRun, baseURL, config, observer string) {
	t.Helper()
	path := filepath.Join(l.root, "codex", "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &hooks); err != nil {
		t.Fatal(err)
	}
	var cfg strings.Builder
	_, _ = fmt.Fprintf(&cfg, "model = \"acceptance\"\nmodel_provider = \"acceptance\"\ncheck_for_update_on_startup = false\n[analytics]\nenabled = false\n[feedback]\nenabled = false\n[model_providers.acceptance]\nname = \"Loopback acceptance\"\nbase_url = %q\nwire_api = \"responses\"\nenv_key = \"OPENAI_API_KEY\"\n", baseURL+"/v1")
	want := ": agent-sessions-capture-v1; exec " + shellwords.Join([]string{observer, "capture-v1", "hook", "--config", config, "--agent", "codex"})
	for event, groups := range hooks.Hooks {
		key := map[string]string{"SessionStart": "session_start", "Stop": "stop", "SubagentStop": "subagent_stop", "PreCompact": "pre_compact", "Interrupt": "interrupt", "SessionEnd": "session_end"}[event]
		if key == "" || len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatal("unexpected candidate Codex schema")
		}
		h := groups[0].Hooks[0]
		if h.Command != want {
			t.Fatalf("unreviewed candidate hook command: %q", h.Command)
		}
		_, _ = fmt.Fprintf(&cfg, "\n[hooks.state.%q]\ntrusted_hash = %q\n", path+":"+key+":0:0", liveCodexIdentity(key, h.Command, h.Async, h.Timeout))
	}
	_, _ = fmt.Fprintf(&cfg, "\n[projects.%q]\ntrust_level = \"trusted\"\n", l.work)
	liveWrite(t, filepath.Join(l.root, "codex", "config.toml"), cfg.String(), 0o600)
}

func captureNativeRead(t *testing.T, l liveRun, agent string) bool {
	t.Helper()
	var currents []string
	if err := filepath.WalkDir(filepath.Join(l.root, "bucket"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !strings.HasPrefix(d.Name(), ".") && (d.Name() == "current.json" || filepath.Base(filepath.Dir(path)) == "long-data") {
			currents = append(currents, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backend := publication.NewLocal(filepath.Join(l.root, "bucket"))
	for _, path := range currents {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var current publication.Current
		if json.Unmarshal(b, &current) != nil || current.ManifestKey == "" {
			continue
		}
		if current.Identity.Agent != agent {
			continue
		}
		manifest, err := publication.Read(t.Context(), backend, current.Identity)
		if err != nil {
			t.Fatal(err)
		}
		mainOK, nativeOK := false, false
		for i := range manifest.Sources {
			source := &manifest.Sources[i]
			if source.Role == "main" {
				if !strings.HasPrefix(source.Path, l.root+"/") {
					t.Fatal("non-scratch source")
				}
				raw, err := os.ReadFile(source.Path)
				if err != nil {
					t.Fatal(err)
				}
				mainOK = liveHasAssistantReply(raw, agent) && source.Object.SHA256 == liveDigest(raw) && source.Object.Size == int64(len(raw))
			}
			if slices.Contains(source.Associations, "enrichment:launch") {
				nativeOK = true
			}
		}
		if !mainOK || !nativeOK {
			continue
		}
		dest, err := os.MkdirTemp(l.root, "export-") //nolint:usetesting // Export must remain inside sandbox scratch, not the test runner TMPDIR.
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(dest); err != nil {
			t.Fatal(err)
		}
		exported, err := publication.ExportSession(t.Context(), backend, current.Identity, dest)
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range exported.Bodies {
			data, err := os.ReadFile(filepath.Join(dest, body.Path))
			if err != nil {
				t.Fatal(err)
			}
			if liveDigest(data) != body.SHA256 {
				t.Fatal("authoritative export hash mismatch")
			}
		}
		captureNativeAnalytics(t, l, dest)
		t.Logf("actual %s -> durable queue -> Local -> authoritative reader/export: publication=%s sources=%d enrichment=%s", agent, manifest.PublicationID, len(manifest.Sources), manifest.Coverage.Enrichment)
		return true
	}
	return false
}

func captureNativeEvents(t *testing.T, l liveRun, agent string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(l.root, "events", "*.input"))
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		event, _ := p["hook_event_name"].(string)
		if agent == "pi" {
			event, _ = p["event"].(string)
		}
		if event == "SessionStart" || event == "session_start" {
			if p["cwd"] != l.work {
				t.Fatalf("native cwd differs from scratch launch: %v", p["cwd"])
			}
			session, _ := p["session_id"].(string)
			if session == "" {
				t.Fatal("native session identity absent")
			}
			key := "transcript_path"
			if agent == "pi" {
				key = "session_file"
				if p["context_origin"] != "extension" || p["model"] != "acceptance" || p["provider"] != "acceptance" {
					t.Fatal("Pi native model/provider/origin mismatch")
				}
			}
			source, _ := p[key].(string)
			if !strings.HasPrefix(source, l.root+"/") {
				t.Fatal("native source not in scratch")
			}
		}
		if event != "" {
			events = append(events, event)
		}
	}
	want := []string{"SessionStart", "Stop", "SessionEnd"}
	if agent == "pi" {
		want = []string{"session_start"}
	}
	for _, event := range want {
		if !slices.Contains(events, event) {
			t.Fatalf("native events=%v missing %s", events, event)
		}
	}
	t.Logf("actual native events: %v", events)
}

// The interpreter is explicitly selected; no ambient credentials, user-site
// packages, bytecode writes or original-path fallback are passed to analytics.
func captureNativeAnalytics(t *testing.T, l liveRun, export string) {
	t.Helper()
	verifier, err := filepath.Abs(filepath.Join("..", "..", "tools", "candidate-native", "verify.py"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(export, "analytics")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	python := os.Getenv("CANDIDATE_NATIVE_ANALYTICS_PYTHON")
	resolved, err := filepath.EvalSymlinks(python)
	if err != nil {
		t.Fatal(err)
	}
	// Permit only the explicitly selected interpreter distribution, its venv,
	// and these three public consumer source files. Never allow the real home
	// or repository subtree: sources, credentials and research remain denied.
	runtimeRoot := filepath.Dir(filepath.Dir(resolved))
	venvRoot := filepath.Dir(filepath.Dir(python))
	if runtimeRoot == "/" || venvRoot == "/" {
		t.Fatal("analytics interpreter must have a scoped runtime directory")
	}
	link, err := os.Readlink(python)
	if err != nil && !errors.Is(err, syscall.EINVAL) {
		t.Fatal(err)
	}
	if link != "" && !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(python), link)
	}
	aliasRoot := runtimeRoot
	if link != "" {
		aliasRoot = filepath.Dir(filepath.Dir(link))
	}
	profile, err := os.ReadFile(l.profile)
	if err != nil {
		t.Fatal(err)
	}
	// Sandbox deny rules dominate allow rules. Exempt precisely these inputs
	// from the /Users deny instead of adding an ineffective broad allow.
	exceptions := fmt.Sprintf(`(require-all (require-not (subpath %q)) (subpath "/Users") (require-not (subpath %q)) (require-not (subpath %q)) (require-not (literal %q)) (require-not (literal %q)) (require-not (literal %q)))`, aliasRoot, runtimeRoot, venvRoot, verifier, filepath.Clean(filepath.Join(filepath.Dir(verifier), "..", "analytics", "publications.py")), filepath.Clean(filepath.Join(filepath.Dir(verifier), "..", "analytics", "events.py")))
	// Traversal requires ancestor stat access, never directory listing/content.
	var metadata strings.Builder
	for _, allowed := range []string{python, resolved, link, verifier, filepath.Join(filepath.Dir(verifier), "..", "analytics", "publications.py")} {
		for parent := filepath.Dir(filepath.Clean(allowed)); strings.HasPrefix(parent, "/Users"); parent = filepath.Dir(parent) {
			_, _ = fmt.Fprintf(&metadata, " (require-not (literal %q))", parent)
		}
	}
	metadataDeny := strings.TrimSuffix(exceptions, ")") + metadata.String() + ")"
	profile = []byte(strings.Replace(string(profile), `(subpath "/Users")`, exceptions, 1))
	profile = []byte(strings.Replace(string(profile), `(deny file-read*`, `(deny file-read-data file-read-xattr`, 1))
	profile = append(profile, []byte("\n(deny file-read-metadata "+metadataDeny+")\n")...)
	profile = append(profile, []byte(`(deny file-read-metadata (subpath "/private/var/root") (subpath "/Library/Keychains") (subpath "/Library/Application Support/ClaudeCode") (subpath "/etc/claude-code") (subpath "/private/etc/claude-code") (subpath "/etc/codex") (subpath "/private/etc/codex"))`)...)
	analytics := l
	analytics.profile = filepath.Join(dir, "analytics-sandbox.sb")
	liveWrite(t, analytics.profile, string(profile), 0o600)
	for _, args := range [][]string{{"/bin/ls", "/Users"}, {"/bin/sh", "-c", "echo unsafe > /tmp/memory-native-analytics-forbidden"}, {"/usr/bin/curl", "--silent", "--max-time", "1", "http://192.0.2.1/"}} {
		probe := analytics.command(ctx, args[0], args[1:]...)
		probe.Env = []string{"PATH=/usr/bin:/bin"}
		if err := probe.Run(); err == nil {
			t.Fatalf("analytics sandbox allowed forbidden probe %q", args)
		}
	}
	cmd := analytics.command(ctx, python, "-I", "-B", verifier, export, filepath.Join(dir, "events.duckdb"))
	cmd.Dir = l.root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1", "HOME=" + filepath.Join(l.root, "home"), "TMPDIR=" + filepath.Join(l.root, "tmp")}
	out, err := cmd.CombinedOutput()
	liveWrite(t, filepath.Join(dir, "verification.log"), string(out), 0o600)
	if err != nil {
		t.Fatalf("actual native analytics consumer: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// A disabled/untrusted hook cannot emit Stop. The shared runHeld intentionally
// requires it, so this held native session waits for a NEW assistant transcript
// instead; it never manufactures a lifecycle event to drive acceptance.
func captureNativeNoHooks(t *testing.T, l liveRun, agent string, args []string, phase string) {
	t.Helper()
	before, err := filepath.Glob(filepath.Join(l.root, "events", "*.argv"))
	if err != nil {
		t.Fatal(err)
	}
	transcripts := captureNativeTranscripts(t, l, agent)
	var out []byte
	if agent == "pi" {
		out = l.run(t, "/opt/homebrew/bin/pi", args...)
	} else {
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		cmd := l.command(ctx, "/usr/bin/script", "-q", "/dev/null", "/bin/sh", "-c", "/bin/stty rows 40 cols 120; exec "+shellwords.Join(append([]string{"/opt/homebrew/bin/" + agent}, args...)))
		outputPath := filepath.Join(l.root, agent+"-"+phase+".output")
		output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := output.Close(); err != nil {
				t.Error(err)
			}
		}()
		cmd.Stdout, cmd.Stderr = output, output
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		time.Sleep(time.Second)
		if _, err := io.WriteString(input, "\x1b[1;1R\x1b[?1;2c"); err != nil {
			t.Fatal(err)
		}
		if agent == "codex" {
			if phase == "wrong-trusted-hash" {
				review := false
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
					screen, err := os.ReadFile(outputPath)
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(screen, []byte("Hooks need review")) {
						review = true
						break
					}
				}
				if !review {
					cancel()
					t.Fatal("Codex did not expose actual hook review prompt")
				}
				// Select the actual native 'Continue without trusting' option. Never
				// approve unknown hashes, invoke a fake hook, or change real trust state.
				for _, key := range []string{"\x1b[B", "\x1b[B", "\r"} {
					if _, err := io.WriteString(input, key); err != nil {
						t.Fatal(err)
					}
					time.Sleep(150 * time.Millisecond)
				}
			}
			time.Sleep(time.Second)
			if _, err := io.WriteString(input, livePrompt); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if _, err := io.WriteString(input, "\r"); err != nil {
				t.Fatal(err)
			}
		}
		replied := false
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if captureNativeNewReply(t, l, agent, transcripts) {
				replied = true
				break
			}
		}
		if !replied {
			cancel()
		} else {
			time.Sleep(300 * time.Millisecond)
			if _, err := io.WriteString(input, map[string]string{"claude": "/exit", "codex": "/quit"}[agent]); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if _, err := io.WriteString(input, "\r"); err != nil {
				t.Fatal(err)
			}
		}
		err = <-done
		_ = input.Close() // Wait has already closed the process pipe.
		if err != nil || !replied {
			t.Fatalf("actual %s %s replied=%t: %v; evidence=%s", agent, phase, replied, err, l.root)
		}
		out, err = os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(out, []byte(liveReply)) || !captureNativeNewReply(t, l, agent, transcripts) {
		t.Fatal("disabled/untrusted native session has no new actual assistant reply")
	}
	after, err := filepath.Glob(filepath.Join(l.root, "events", "*.argv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("%s candidate hook invoked: before=%d after=%d", phase, len(before), len(after))
	}
	t.Logf("actual %s %s: new assistant reply; candidate hook invocation count unchanged (%d)", agent, phase, len(after))
}

func captureNativeTranscripts(t *testing.T, l liveRun, agent string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	if err := filepath.WalkDir(filepath.Join(l.root, agent), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			found[path] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

func captureNativeNewReply(t *testing.T, l liveRun, agent string, before map[string]bool) bool {
	t.Helper()
	for path := range captureNativeTranscripts(t, l, agent) {
		if before[path] {
			continue
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if liveHasAssistantReply(data, agent) {
			return true
		}
	}
	return false
}
