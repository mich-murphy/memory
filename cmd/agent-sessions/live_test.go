package main

// Live-agent acceptance is deliberately opt-in. These tests never inherit the caller's
// environment, install into a real home, or use R2. The sandbox also covers detached hooks.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/shellwords"
)

const (
	livePrompt = "Reply with exactly ACCEPTANCE_OK. Do not use tools."
	liveReply  = "ACCEPTANCE_OK"
)

// Only these routes exist. Unexpected traffic is evidence of a configuration error,
// not something to proxy to a real service.
type liveModel struct {
	mu         sync.Mutex
	requests   []string
	unexpected []string
}

func (m *liveModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		return
	}
	m.mu.Lock()
	m.requests = append(m.requests, r.Method+" "+r.URL.Path)
	valid := r.Method == http.MethodPost && (r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/responses") && bytes.Contains(body, []byte(livePrompt))
	if !valid {
		m.unexpected = append(m.unexpected, r.Method+" "+r.URL.Path)
	}
	m.mu.Unlock()
	if !valid {
		http.Error(w, "unexpected acceptance request", http.StatusBadRequest)
		return
	}
	// Give asynchronous SessionStart time to run while its agent ancestor is alive.
	time.Sleep(time.Second)
	w.Header().Set("Content-Type", "text/event-stream")
	var events []string
	if r.URL.Path == "/v1/messages" {
		events = []string{
			`{"type":"message_start","message":{"id":"msg_acceptance","type":"message","role":"assistant","model":"acceptance","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ACCEPTANCE_OK"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`,
			`{"type":"message_stop"}`,
		}
	} else {
		item := `{"id":"msg_acceptance","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ACCEPTANCE_OK","annotations":[]}]}`
		events = []string{
			`{"type":"response.created","response":{"id":"resp_acceptance","object":"response","status":"in_progress","output":[]}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_acceptance","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
			`{"type":"response.content_part.added","item_id":"msg_acceptance","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
			`{"type":"response.output_text.delta","item_id":"msg_acceptance","output_index":0,"content_index":0,"delta":"ACCEPTANCE_OK"}`,
			`{"type":"response.output_text.done","item_id":"msg_acceptance","output_index":0,"content_index":0,"text":"ACCEPTANCE_OK"}`,
			`{"type":"response.output_item.done","output_index":0,"item":` + item + `}`,
			`{"type":"response.completed","response":{"id":"resp_acceptance","object":"response","status":"completed","output":[` + item + `],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`,
		}
	}
	for _, event := range events {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(event), &header); err != nil {
			panic(err)
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", header.Type, event); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func TestLiveModelProtocols(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/wrong"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			m := &liveModel{}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(`{"input":"`+livePrompt+`"}`))
			w := httptest.NewRecorder()
			m.ServeHTTP(w, req)
			if path == "/wrong" {
				if w.Code != http.StatusBadRequest || len(m.unexpected) != 1 {
					t.Fatal("unknown route accepted")
				}
				return
			}
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), liveReply) {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
			chunks := strings.Split(strings.TrimSpace(w.Body.String()), "\n\n")
			for _, chunk := range chunks {
				lines := strings.Split(chunk, "\n")
				var event map[string]any
				if len(lines) != 2 || json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &event) != nil || lines[0] != "event: "+event["type"].(string) {
					t.Fatalf("malformed SSE: %s", chunk)
				}
			}
			last := chunks[len(chunks)-1]
			if !strings.Contains(last, "message_stop") && !strings.Contains(last, "response.completed") {
				t.Fatalf("stream does not finish: %s", last)
			}
		})
	}
	m := &liveModel{}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/messages", strings.NewReader(`{}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatal("missing prompt accepted")
	}
}

type liveRun struct {
	root    string
	env     []string
	profile string
	work    string
}

func liveWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func (l liveRun) command(ctx context.Context, binary string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-f", l.profile, binary}, args...)...)
	cmd.Env, cmd.Dir = l.env, l.work
	return cmd
}

func (l liveRun) run(t *testing.T, binary string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := l.command(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	liveWrite(t, filepath.Join(l.root, filepath.Base(binary)+".output"), string(out), 0o600)
	if err != nil {
		t.Fatalf("%s %q: %v\n%s\nEvidence: %s", binary, args, err, out, l.root)
	}
	return out
}

func newLiveRun(t *testing.T, port string) liveRun {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "memory-live-") //nolint:usetesting // Retain failed live-agent evidence; t.TempDir removes it even on failure.
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	// Keep evidence on failure; successes may also be retained explicitly.
	t.Cleanup(func() {
		if !t.Failed() && os.Getenv("AGENT_SESSIONS_LIVE_KEEP") != "1" {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		}
	})
	l := liveRun{root: root, work: filepath.Join(root, "work"), profile: filepath.Join(root, "sandbox.sb")}
	for _, dir := range []string{"home", "work", "bin", "tmp", "coverage", "events", "claude", "codex", "pi", "state", "bucket"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	l.env = []string{
		"PATH=" + filepath.Join(root, "bin") + ":/opt/homebrew/bin:/usr/bin:/bin", "SHELL=/bin/sh", "LANG=en_US.UTF-8", "TERM=xterm-256color",
		"HOME=" + filepath.Join(root, "home"), "TMPDIR=" + filepath.Join(root, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(root, "home", ".config"), "XDG_CACHE_HOME=" + filepath.Join(root, "home", ".cache"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude"), "CODEX_HOME=" + filepath.Join(root, "codex"), "PI_CODING_AGENT_DIR=" + filepath.Join(root, "pi"),
		"AGENT_SESSIONS_CONFIG=" + filepath.Join(root, "config.env"), "AGENT_SESSIONS_STATE_DIR=" + filepath.Join(root, "state"), "AGENT_SESSIONS_LOCAL_STORE=" + filepath.Join(root, "bucket"), "AGENT_SESSIONS_MACHINE_ID=live-acceptance",
		"GOCOVERDIR=" + filepath.Join(root, "coverage"), "ANTHROPIC_API_KEY=synthetic-acceptance-key", "OPENAI_API_KEY=synthetic-acceptance-key",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:" + port, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1", "CLAUDE_CODE_ENABLE_TELEMETRY=0", "PI_OFFLINE=1",
	}
	// The default deny on networking is inherited by every child, including detached syncs.
	// Reads of real homes and writes outside scratch are forbidden even if an agent ignores HOME.
	profile := fmt.Sprintf(`(version 1)
(allow default)
(deny network*)
(allow network-outbound (remote ip "localhost:%s"))
(deny file-read* (subpath "/Users") (subpath "/private/var/root")
 (subpath "/Library/Keychains") (subpath "/Library/Application Support/ClaudeCode")
 (subpath "/etc/claude-code") (subpath "/private/etc/claude-code")
 (subpath "/etc/codex") (subpath "/private/etc/codex"))
(deny mach-lookup (global-name "com.apple.securityd") (global-name "com.apple.SecurityServer"))
(deny file-write*)
(allow file-write* (subpath %q) (literal "/dev/null") (literal "/dev/ptmx") (regex #"^/dev/ttys[0-9]+$"))
`, port, root)
	liveWrite(t, l.profile, profile, 0o600)
	liveWrite(t, filepath.Join(root, "config.env"), "", 0o600)
	// A transparent observer: the installed hooks/extension still execute the collector,
	// with unchanged arguments and payload, and the real agent remains its ancestor.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := fmt.Sprintf("#!/bin/sh\nexport MEMORY_LIVE_OBSERVER_ROOT=%s\nexport MEMORY_LIVE_OBSERVER_BINARY=%s\nexec %s -test.run=^TestLiveHookObserver$ -- \"$@\"\n", root, filepath.Join(binDir, "agent-sessions"), executable)
	liveWrite(t, filepath.Join(root, "bin", "agent-sessions"), wrapper, 0o700)
	t.Logf("scratch evidence: %s", root)
	return l
}

func (l liveRun) verifyIsolation(t *testing.T, port string) {
	t.Helper()
	l.run(t, "/usr/bin/curl", "--silent", "--fail", "--max-time", "3", "http://127.0.0.1:"+port+"/health")
	for _, args := range [][]string{
		{"/usr/bin/curl", "--silent", "--max-time", "1", "http://192.0.2.1/"},
		{"/bin/sh", "-c", "echo unsafe > /tmp/memory-live-forbidden"},
		{"/bin/ls", "/Users"},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		out, err := l.command(ctx, args[0], args[1:]...).CombinedOutput()
		cancel()
		if err == nil {
			t.Fatalf("sandbox did not reject %q: %s", args, out)
		}
	}
}

// Independent fixture format pinned to openai/codex@806d973 discovery.rs and
// its serde schema. Not install.codexHookHash: that is the code under acceptance.
func liveCodexIdentity(event, command string, async bool, timeout int) string {
	canonical := fmt.Sprintf(`{"event_name":%q,"hooks":[{"async":%t,"command":%q,"timeout":%d,"type":"command"}]}`, event, async, command, timeout)
	sha := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("sha256:%x", sha)
}

func (l liveRun) trustCodex(t *testing.T, baseURL string) {
	t.Helper()
	path := filepath.Join(l.root, "codex", "hooks.json")
	data, err := os.ReadFile(path)
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
	if err := json.Unmarshal(data, &hooks); err != nil {
		t.Fatal(err)
	}
	var cfg strings.Builder
	if _, err := fmt.Fprintf(&cfg, "model = \"acceptance\"\nmodel_provider = \"acceptance\"\ncheck_for_update_on_startup = false\n[analytics]\nenabled = false\n[feedback]\nenabled = false\n[model_providers.acceptance]\nname = \"Loopback acceptance\"\nbase_url = %q\nwire_api = \"responses\"\nenv_key = \"OPENAI_API_KEY\"\n", baseURL+"/v1"); err != nil {
		t.Fatal(err)
	}
	for event, groups := range hooks.Hooks {
		key := map[string]string{"SessionStart": "session_start", "Stop": "stop", "SubagentStop": "subagent_stop", "PreCompact": "pre_compact", "Interrupt": "interrupt", "SessionEnd": "session_end"}[event]
		if key == "" || len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("unexpected installed hook schema: %s", data)
		}
		h := groups[0].Hooks[0]
		if h.Command != filepath.Join(l.root, "bin", "agent-sessions")+" hook codex" {
			t.Fatalf("unreviewed hook command: %q", h.Command)
		}
		if _, err := fmt.Fprintf(&cfg, "\n[hooks.state.%q]\ntrusted_hash = %q\n", path+":"+key+":0:0", liveCodexIdentity(key, h.Command, h.Async, h.Timeout)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fmt.Fprintf(&cfg, "\n[projects.%q]\ntrust_level = \"trusted\"\n", l.work); err != nil {
		t.Fatal(err)
	}
	liveWrite(t, filepath.Join(l.root, "codex", "config.toml"), cfg.String(), 0o600)
}

func TestLiveAgents(t *testing.T) {
	if os.Getenv("AGENT_SESSIONS_LIVE") != "1" {
		t.Skip("opt in with AGENT_SESSIONS_LIVE=1; see docs/live-acceptance.md")
	}
	if runtime.GOOS != "darwin" {
		t.Fatal("live acceptance requires the macOS sandbox-exec profile; do not run unsandboxed")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex", "pi"} {
		t.Run(agent, func(t *testing.T) {
			m := &liveModel{}
			server := httptest.NewServer(m)
			defer server.Close()
			_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			l := newLiveRun(t, port)
			l.verifyIsolation(t, port)
			l.run(t, filepath.Join(l.root, "bin", "agent-sessions"), "install")
			binary := "/opt/homebrew/bin/" + agent
			version := strings.TrimSpace(string(l.run(t, binary, "--version")))
			t.Logf("installed %s", version)
			var args []string
			switch agent {
			case "claude":
				// Claude 2.1.296 remembers API-key approval by its last 20 characters.
				// This approves only the synthetic fixture key, in the scratch config.
				onboarding, err := json.Marshal(map[string]any{"hasCompletedOnboarding": true, "theme": "dark", "customApiKeyResponses": map[string]any{"approved": []string{"hetic-acceptance-key"}, "rejected": []string{}}, "projects": map[string]any{l.work: map[string]any{"hasTrustDialogAccepted": true}}})
				if err != nil {
					t.Fatal(err)
				}
				liveWrite(t, filepath.Join(l.root, "claude", ".claude.json"), string(onboarding), 0o600)
				args = []string{"--debug-file", filepath.Join(l.root, "claude-debug.log"), "--permission-mode", "default", "--model", "claude-sonnet-4-6", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--", livePrompt}
			case "codex":
				l.trustCodex(t, server.URL)
				args = []string{"--no-alt-screen", "--sandbox", "read-only", "--disable", "shell_tool", "-c", `web_search="disabled"`}
			case "pi":
				liveWrite(t, filepath.Join(l.root, "pi", "models.json"), fmt.Sprintf(`{"providers":{"acceptance":{"baseUrl":%q,"api":"anthropic-messages","apiKey":"synthetic-acceptance-key","models":[{"id":"acceptance","name":"Acceptance","reasoning":false,"input":["text"],"contextWindow":32000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, server.URL), 0o600)
				args = []string{"--print", "--offline", "--provider", "acceptance", "--model", "acceptance", "--thinking", "off", "--no-tools", "--no-mcp", "--no-skills", "--no-prompt-templates", "--no-themes", livePrompt}
			}
			started := time.Now()
			var out []byte
			switch agent {
			case "codex", "claude":
				out = l.runHeld(t, agent, binary, args)
			default:
				out = l.run(t, binary, args...)
			}
			ended := time.Now()
			if !bytes.Contains(out, []byte(liveReply)) {
				t.Fatalf("agent returned no acceptance reply: %s", out)
			}
			m.mu.Lock()
			requests, unexpected := slices.Clone(m.requests), slices.Clone(m.unexpected)
			m.mu.Unlock()
			if len(requests) == 0 || len(unexpected) != 0 {
				t.Fatalf("model requests = %v; unexpected = %v", requests, unexpected)
			}
			t.Logf("model requests: %v", requests)
			// Do not call sync/backfill here: only actual installed hooks may populate the store.
			deadline := time.Now().Add(35 * time.Second)
			for !liveVerifyArchive(t, l.root, agent, args, started, ended) {
				if time.Now().After(deadline) {
					t.Fatalf("hooks did not archive final transcript and launch; evidence: %s", l.root)
				}
				time.Sleep(250 * time.Millisecond)
			}
			liveVerifyEvents(t, l.root, agent)
		})
	}
}

func liveVerifyEvents(t *testing.T, root, agent string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "events", "*.argv"))
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, file := range files {
		argv, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(argv, []byte("hook\n"+agent+"\n")) {
			continue
		}
		input, err := os.ReadFile(strings.TrimSuffix(file, ".argv") + ".input")
		if err != nil {
			t.Fatal(err)
		}
		if agent == "pi" {
			words := strings.Split(string(argv), "\n")
			for i, word := range words {
				if word == "--event" && i+1 < len(words) {
					events = append(events, words[i+1])
				}
			}
		} else {
			var payload struct {
				Event string `json:"hook_event_name"`
			}
			if err := json.Unmarshal(input, &payload); err != nil {
				t.Fatalf("hook payload: %v", err)
			}
			events = append(events, payload.Event)
		}
	}
	want := []string{"SessionStart", "Stop", "SessionEnd"}
	if agent == "pi" {
		want = []string{"session_start", "agent_settled", "session_shutdown"}
	}
	for _, event := range want {
		if !slices.Contains(events, event) {
			t.Fatalf("actual %s hook events = %v, missing %s", agent, events, event)
		}
	}
	t.Logf("actual hook process events: %v", events)
}

// Archive-format seam: v2 manifests and v3 immutable publication records.
// It checks source bytes AFTER exit against the objects selected by the manifest,
// without invoking any collector sync. A matching reply alone is not acceptance.
func liveVerifyArchive(t *testing.T, root, agent string, args []string, started, ended time.Time) bool {
	t.Helper()
	bucket := filepath.Join(root, "bucket")
	if _, err := os.Stat(filepath.Join(bucket, "v3")); err == nil {
		return liveVerifyV3(t, root, agent, args, started, ended)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var manifests []string
	if err := filepath.WalkDir(filepath.Join(bucket, "manifests"), func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") {
			manifests = append(manifests, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range manifests {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Agent string `json:"agent"`
			Info  struct {
				Root string `json:"source_root"`
			} `json:"info"`
			Launch struct {
				Recorded float64 `json:"recorded_at"`
				Process  struct {
					Argv []string `json:"argv"`
				} `json:"process"`
			} `json:"launch"`
			Snapshots []struct {
				Timing string  `json:"timing"`
				Taken  float64 `json:"taken_at"`
			} `json:"snapshots"`
			Transcripts []struct {
				Key    string `json:"key"`
				Role   string `json:"role"`
				SHA256 string `json:"sha256"`
				Size   int64  `json:"size"`
				Pieces []struct {
					Object string `json:"object"`
					Start  int64  `json:"start"`
					End    int64  `json:"end"`
				} `json:"pieces"`
			} `json:"transcripts"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Agent != agent {
			continue
		}
		for _, transcript := range manifest.Transcripts {
			if transcript.Role != "main" {
				continue
			}
			key := transcript.Key
			if strings.HasPrefix(key, "roots/") {
				parts := strings.SplitN(key, "/", 3)
				if len(parts) != 3 {
					t.Fatal("invalid alternate-root key")
				}
				key = parts[2]
			}
			source := filepath.Join(manifest.Info.Root, key)
			if agent == "codex" {
				matches, err := filepath.Glob(filepath.Join(root, "codex", "sessions", "*", "*", "*", filepath.Base(transcript.Key)))
				if err != nil {
					t.Fatal(err)
				}
				if len(matches) != 1 {
					continue
				}
				source = matches[0]
			}
			want, err := os.ReadFile(source)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if !liveHasAssistantReply(want, agent) {
				continue
			}
			var got []byte
			for _, piece := range transcript.Pieces {
				encoded, err := os.ReadFile(filepath.Join(bucket, piece.Object))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := raw.Decompress(encoded)
				if err != nil {
					t.Fatal(err)
				}
				if piece.Start != int64(len(got)) || piece.End-piece.Start != int64(len(decoded)) {
					t.Fatal("noncontiguous archive pieces")
				}
				got = append(got, decoded...)
			}
			if !bytes.Equal(got, want) {
				continue
			}
			sha := sha256.Sum256(want)
			if transcript.SHA256 != hex.EncodeToString(sha[:]) || transcript.Size != int64(len(want)) {
				t.Fatal("manifest hash/size differs from source")
			}
			argv := manifest.Launch.Process.Argv
			if len(argv) == 0 {
				continue
			}
			if len(argv) < len(args)+1 || !slices.Equal(argv[len(argv)-len(args):], args) {
				t.Fatalf("recorded argv = %q, want exact launched suffix %q", argv, args)
			}
			lower := float64(started.UnixNano()) / 1e9
			upper := float64(ended.UnixNano()) / 1e9
			if manifest.Launch.Recorded < lower || manifest.Launch.Recorded > upper {
				t.Fatalf("launch recorded_at = %f, outside agent execution [%f,%f]", manifest.Launch.Recorded, lower, upper)
			}
			pre := false
			for _, snapshot := range manifest.Snapshots {
				if snapshot.Timing == "pre_session" {
					pre = true
					if snapshot.Taken < manifest.Launch.Recorded {
						t.Fatal("snapshot predates recorded launch")
					}
				}
			}
			if !pre {
				t.Fatal("session-start snapshot missing")
			}
			t.Logf("byte-exact %s transcript: %d bytes sha256=%x; argv=%q; launch=%f; pre_session snapshot", agent, len(want), sha, argv, manifest.Launch.Recorded)
			return true
		}
	}
	return false
}

// This subprocess-only observer decodes one JSON object rather than waiting for EOF:
// Codex intentionally leaves the pipe open. Run before TestMain, which builds the
// collector; an agent's isolated environment intentionally has no Go toolchain.
func init() {
	if os.Getenv("MEMORY_LIVE_OBSERVER_ROOT") == "" {
		return
	}
	if err := liveObserve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func liveObserve() error {
	root := os.Getenv("MEMORY_LIVE_OBSERVER_ROOT")
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return errors.New("observer requires argv separator")
	}
	args := os.Args[separator+1:]
	f, err := os.CreateTemp(filepath.Join(root, "events"), "invocation.*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(name+".argv", []byte(strings.Join(args, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	var payload json.RawMessage
	if len(args) > 0 && args[0] == "hook" && len(args) > 1 && args[1] != "pi" {
		if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
			return err
		}
	}
	if err := os.WriteFile(name+".input", payload, 0o600); err != nil {
		return err
	}
	input, err := os.Open(name + ".input")
	if err != nil {
		return err
	}
	if err := unix.Dup2(int(input.Fd()), 0); err != nil {
		_ = input.Close()
		return err
	} // read-only on error
	_ = input.Close() // read-only file; stdin now owns a duplicate
	binary := os.Getenv("MEMORY_LIVE_OBSERVER_BINARY")
	return syscall.Exec(binary, append([]string{binary}, args...), os.Environ())
}

// Print/exec teardown can cancel async hooks even after a successful result.
// Keep real interactive CLIs open through Stop, issue /exit or /quit, and wait
// for orderly process exit before closing input. Never manufacture an end hook.
func (l liveRun) runHeld(t *testing.T, agent, binary string, args []string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := l.command(ctx, "/usr/bin/script", "-q", "/dev/null", "/bin/sh", "-c", "/bin/stty rows 40 cols 120; exec "+shellwords.Join(append([]string{binary}, args...)))
	outputPath := filepath.Join(l.root, agent+"-interactive.output")
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
		time.Sleep(time.Second)
		if _, err := io.WriteString(input, livePrompt); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		if _, err := io.WriteString(input, "\r"); err != nil {
			t.Fatal(err)
		}
	}
	stopped := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		files, err := filepath.Glob(filepath.Join(l.root, "events", "*.input"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(body, []byte(`"hook_event_name":"Stop"`)) {
				stopped = true
			}
		}

		if stopped {
			break
		}
	}
	if !stopped {
		cancel()
	}

	if stopped {
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
	_ = input.Close() // Wait closed the read/write pipe after the real agent exited.
	out, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err != nil || !stopped {
		t.Fatalf("held %s Stop=%t: %v\n%s\nEvidence: %s", agent, stopped, err, out, l.root)
	}
	return out
}

// Deliberately decode the wire format, not syncer/state's private Go types. The
// same harness can be cherry-picked before or after archive v3 integration.
type livePublication struct {
	Schema   int    `json:"schema"`
	Kind     string `json:"kind"`
	Agent    string `json:"agent"`
	Key      string `json:"key"`
	Sequence uint64 `json:"sequence"`
	Parent   string `json:"parent"`
	File     *struct {
		Identity struct {
			Agent      string `json:"agent"`
			SessionKey string `json:"session_key"`
		} `json:"identity"`
		Sources []struct {
			Path string `json:"path"`
		} `json:"sources"`
		Role   string `json:"role"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
		Pieces []struct {
			Object        string `json:"object"`
			Start         int64  `json:"start"`
			End           int64  `json:"end"`
			SHA256        string `json:"sha256"`
			EncodedSHA256 string `json:"encoded_sha256"`
		} `json:"pieces"`
	} `json:"file"`
	Launch *struct {
		SessionKey string `json:"session_key"`
		Envelope   struct {
			ID         string  `json:"id"`
			RecordedAt float64 `json:"recorded_at"`
			Payload    struct {
				Process struct {
					Argv []string `json:"argv"`
				} `json:"process"`
			} `json:"payload"`
		} `json:"envelope"`
		Snapshot struct {
			Timing  string  `json:"timing"`
			TakenAt float64 `json:"taken_at"`
		} `json:"snapshot"`
	} `json:"launch"`
}

func liveDigest(body []byte) string {
	sha := sha256.Sum256(body)
	return hex.EncodeToString(sha[:])
}

// A unique parent/sequence chain, not largest sequence or timestamp, selects tip.
// Plans are never admitted; a missing parent, duplicate sequence or branch fails.
func liveCommittedTips(bucket string) ([]livePublication, error) {
	files, err := filepath.Glob(filepath.Join(bucket, "v3", "machines", "live-acceptance", "records", "*", "*", "commits", "*.json"))
	if err != nil {
		return nil, err
	}
	groups := map[string]map[string]livePublication{}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var p livePublication
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		if p.Schema != 3 || p.Sequence == 0 {
			return nil, errors.New("invalid v3 publication")
		}
		id := liveDigest(body)
		if filepath.Base(path) != fmt.Sprintf("%020d-%s.json", p.Sequence, id) {
			return nil, errors.New("publication filename differs from content hash")
		}
		stream := filepath.Dir(filepath.Dir(path))
		if groups[stream] == nil {
			groups[stream] = map[string]livePublication{}
		}
		groups[stream][id] = p
	}
	var tips []livePublication
	for _, records := range groups {
		parent := ""
		sequence := uint64(1)
		var tip livePublication
		consumed := 0
		for {
			id := ""
			for sha, p := range records {
				if p.Parent != parent {
					continue
				}
				if id != "" {
					return nil, errors.New("branched committed chain")
				}
				if p.Sequence != sequence {
					return nil, errors.New("noncontiguous committed sequence")
				}
				id, tip = sha, p
			}
			if id == "" {
				break
			}
			parent = id
			sequence++
			consumed++
		}
		if consumed != len(records) {
			return nil, errors.New("committed chain has missing parent")
		}
		tips = append(tips, tip)
	}
	return tips, nil
}

func liveVerifyV3(t *testing.T, root, agent string, args []string, started, ended time.Time) bool {
	t.Helper()
	bucket := filepath.Join(root, "bucket")
	tips, err := liveCommittedTips(bucket)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range tips {
		if p.Agent != agent || p.File == nil || p.File.Role != "main" {
			continue
		}
		var source string
		for _, s := range p.File.Sources {
			if strings.HasPrefix(s.Path, root+string(filepath.Separator)) {
				source = s.Path
				break
			}
		}
		if source == "" {
			t.Fatal("main transcript has no scratch source")
		}
		want, err := os.ReadFile(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !liveHasAssistantReply(want, agent) || p.File.SHA256 != liveDigest(want) || p.File.Size != int64(len(want)) {
			continue
		}
		var got []byte
		for _, piece := range p.File.Pieces {
			if !strings.HasPrefix(piece.Object, "v3/blobs/zstd/") || strings.Contains(piece.Object, "..") {
				t.Fatal("unsafe blob key")
			}
			encoded, err := os.ReadFile(filepath.Join(bucket, filepath.FromSlash(piece.Object)))
			if err != nil {
				t.Fatal(err)
			}
			if liveDigest(encoded) != piece.EncodedSHA256 {
				t.Fatal("encoded piece checksum differs")
			}
			decoded, err := raw.Decompress(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if liveDigest(decoded) != piece.SHA256 || piece.Start != int64(len(got)) || piece.End-piece.Start != int64(len(decoded)) {
				t.Fatal("invalid decoded piece range/checksum")
			}
			got = append(got, decoded...)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("v3 source and reconstructed committed generation differ")
		}
		for _, launch := range tips {
			if launch.Agent != agent || launch.Launch == nil || launch.Launch.SessionKey != p.File.Identity.SessionKey {
				continue
			}
			envelope := launch.Launch.Envelope
			argv := envelope.Payload.Process.Argv
			if len(argv) < len(args)+1 || !slices.Equal(argv[len(argv)-len(args):], args) {
				t.Fatalf("recorded argv = %q, want exact launched suffix %q", argv, args)
			}
			lower, upper := float64(started.UnixNano())/1e9, float64(ended.UnixNano())/1e9
			if envelope.ID == "" || envelope.RecordedAt < lower || envelope.RecordedAt > upper {
				t.Fatal("v3 launch identity/time differs from actual agent execution")
			}
			snapshot := launch.Launch.Snapshot
			// V3 says captured, not pre_session: async launch hooks do not prove
			// pre-prompt capture. Record the actual read time without overstating it.
			if snapshot.Timing != "captured" || snapshot.TakenAt < envelope.RecordedAt || snapshot.TakenAt > float64(time.Now().UnixNano())/1e9 {
				t.Fatalf("incorrect v3 capture timing: %+v", snapshot)
			}
			t.Logf("byte-exact v3 %s: %d bytes sha256=%s; argv=%q; launch=%f; captured=%f", agent, len(want), p.File.SHA256, argv, envelope.RecordedAt, snapshot.TakenAt)
			return true
		}
	}
	return false
}

func TestLiveCodexIdentitiesMatchIndependentRust(t *testing.T) {
	// Generated by the independent pinned Rust schema/normalizer, not collector code.
	fixtures := []struct {
		Event   string
		Async   bool
		Timeout int
		Hash    string
	}{
		{"session_start", true, 30, "f9aaf651bc72ec2c7ece723ed72173d57025a42c0c4aeb0ce58f08eaa87e81f0"},
		{"stop", true, 30, "b6c847179ffae4ff1a75a0bb60a27609c13b6bd56d5867392d3ae92a1bcd894f"},
		{"subagent_stop", true, 30, "2e86545241e58f82b1e229ff07648cd8267cb1e564c785808e8b652ea9b74af9"},
		{"pre_compact", true, 30, "c82af6d9a41cd0fa36027698059e1d349d68095a4bc902dce9e0466375eccf9a"},
		{"interrupt", true, 3, "1d507a4562d1cd9918aebc652cb58a9d0869414c83fe2ad9f12cd873738e8eb2"},
		{"session_end", false, 3, "01f2bf80cd2142f0f6fe3133cbc168d200e7c8150172cbf48c8b845647b85d1b"},
	}
	for _, f := range fixtures {
		if got := liveCodexIdentity(f.Event, "/fixture/agent-sessions hook codex", f.Async, f.Timeout); got != "sha256:"+f.Hash {
			t.Errorf("identity(%s) = %s, want %s", f.Event, got, f.Hash)
		}
	}
}

func TestLiveObserverDoesNotWaitForEOF(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "events"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--", "hook", "codex")
	cmd.Env = []string{"MEMORY_LIVE_OBSERVER_ROOT=" + root, "MEMORY_LIVE_OBSERVER_BINARY=/usr/bin/true"}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }() // read end may already be closed by exec
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(input, `{"hook_event_name":"Stop","transcript_path":"/scratch/session.jsonl"}`); err != nil {
		t.Fatal(err)
	}
	// Do not close input until Wait has returned: this is the real Codex pipe behavior.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("observer without EOF: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(root, "events", "*.input"))
	if err != nil || len(files) != 1 {
		t.Fatalf("observer payload files = %v: %v", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"hook_event_name":"Stop","transcript_path":"/scratch/session.jsonl"}` {
		t.Fatalf("observer payload = %s", body)
	}
}

func TestLiveCommittedChainRejectsBranchesAndOrphans(t *testing.T) {
	for _, scenario := range []string{"chain", "branch", "orphan", "uncommitted", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			bucket := t.TempDir()
			write := func(sequence uint64, parent, kind string, commit bool) string {
				p := livePublication{Schema: 3, Kind: kind, Agent: "pi", Key: "session", Sequence: sequence, Parent: parent}
				body, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				id := liveDigest(body)
				stage := "commits"
				if !commit {
					stage = "plans"
				}
				path := filepath.Join(bucket, "v3", "machines", "live-acceptance", "records", "file", "stream", stage, fmt.Sprintf("%020d-%s.json", sequence, id))
				liveWrite(t, path, string(body), 0o600)
				if scenario == "corrupt" {
					liveWrite(t, path, "{}", 0o600)
				}
				return id
			}
			first := write(1, "", "file", scenario != "uncommitted")
			switch scenario {
			case "chain":
				write(2, first, "file", true)
			case "branch":
				write(2, first, "file", true)
				write(2, first, "different", true)
			case "orphan":
				write(2, strings.Repeat("0", 64), "file", true)
			}
			tips, err := liveCommittedTips(bucket)
			switch scenario {
			case "chain":
				if err != nil || len(tips) != 1 || tips[0].Sequence != 2 {
					t.Fatalf("tips = %+v: %v", tips, err)
				}
			case "uncommitted":
				if err != nil || len(tips) != 0 {
					t.Fatalf("plan selected: %+v: %v", tips, err)
				}
			default:
				if err == nil {
					t.Fatalf("invalid %s archive accepted: %+v", scenario, tips)
				}
			}
		})
	}
}

func TestLiveV3ReconstructsFinalSourceAndLaunch(t *testing.T) {
	root := t.TempDir()
	bucket := filepath.Join(root, "bucket")
	source := filepath.Join(root, "pi", "sessions", "session.jsonl")
	original := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"acceptance"}]}}` + "\n" + `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"ACCEPTANCE_OK"}]}}` + "\n"
	liveWrite(t, source, original, 0o600)
	var pieces []map[string]any
	start := 0
	for _, chunk := range strings.SplitAfter(original, "\n") {
		if chunk == "" {
			continue
		}
		encoded := raw.Compress([]byte(chunk))
		sha := liveDigest(encoded)
		object := "v3/blobs/zstd/" + sha + ".zst"
		path := filepath.Join(bucket, filepath.FromSlash(object))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		pieces = append(pieces, map[string]any{"object": object, "start": start, "end": start + len(chunk), "sha256": liveDigest([]byte(chunk)), "encoded_sha256": sha})
		start += len(chunk)
	}
	publish := func(kind, key string, payload map[string]any) {
		p := map[string]any{"schema": 3, "machine_id": "live-acceptance", "kind": kind, "agent": "pi", "key": key, "sequence": 1, kind: payload}
		body, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(bucket, "v3", "machines", "live-acceptance", "records", kind, liveDigest([]byte("pi\x00"+key)), "commits", fmt.Sprintf("%020d-%s.json", 1, liveDigest(body)))
		liveWrite(t, path, string(body), 0o600)
	}
	publish("file", "sessions/session.jsonl", map[string]any{
		"identity": map[string]any{"agent": "pi", "session_key": "session"}, "sources": []map[string]string{{"path": source}}, "role": "main", "size": len(original), "sha256": liveDigest([]byte(original)), "pieces": pieces,
	})
	publish("launch", "launch-id", map[string]any{
		"session_key": "session", "envelope": map[string]any{"id": "launch-id", "recorded_at": 15.0, "payload": map[string]any{"process": map[string]any{"argv": []string{"node", "pi", "--print", "prompt"}}}}, "snapshot": map[string]any{"timing": "captured", "taken_at": 16.0},
	})
	if !liveVerifyV3(t, root, "pi", []string{"--print", "prompt"}, time.Unix(10, 0), time.Unix(20, 0)) {
		t.Fatal("matching final source/launch not accepted")
	}
	liveWrite(t, source, original+"{\"late\":true}\n", 0o600)
	if liveVerifyV3(t, root, "pi", []string{"--print", "prompt"}, time.Unix(10, 0), time.Unix(20, 0)) {
		t.Fatal("stale generation accepted after final source grew")
	}
}

// The prompt itself contains ACCEPTANCE_OK. Only an actual assistant response
// proves the real agent consumed the fake model stream; string search is not enough.
func liveHasAssistantReply(source []byte, agent string) bool {
	type message struct {
		Role    string `json:"role"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	for line := range bytes.SplitSeq(source, []byte{'\n'}) {
		var record struct {
			Type    string  `json:"type"`
			Message message `json:"message"`
			Payload message `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		reply := record.Message
		if agent == "codex" {
			if record.Type != "response_item" {
				continue
			}
			reply = record.Payload
		}
		if reply.Role != "assistant" {
			continue
		}
		for _, content := range reply.Content {
			if content.Text == liveReply {
				return true
			}
		}
	}
	return false
}

func TestLiveReplyRequiresAssistantNotPromptEcho(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "pi"} {
		t.Run(agent, func(t *testing.T) {
			field, kind := "message", "message"
			if agent == "claude" {
				kind = "assistant"
			}
			if agent == "codex" {
				field, kind = "payload", "response_item"
			}
			for _, role := range []string{"user", "assistant"} {
				record := fmt.Sprintf(`{"type":%q,%q:{"role":%q,"content":[{"type":"text","text":"ACCEPTANCE_OK"}]}}`, kind, field, role)
				if got := liveHasAssistantReply([]byte(record+"\n"), agent); got != (role == "assistant") {
					t.Fatalf("assistant reply(%s) = %t", role, got)
				}
			}
			if liveHasAssistantReply([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"text":"wrong"}]}}`), agent) {
				t.Fatal("wrong reply accepted")
			}
		})
	}
}
