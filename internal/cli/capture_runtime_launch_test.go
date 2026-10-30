package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturehooks"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestNativeInputStrictBounds(t *testing.T) {
	for _, body := range []string{"null", "{} {}", `{"session_path":2}`, `{"session_path":"/a","extra":"` + strings.Repeat("a", 64<<10) + `"}`} {
		if _, err := decodeNativeCapturePaths(strings.NewReader(body)); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	paths, err := decodeNativeCapturePaths(strings.NewReader(`{"session_file":"/a"}`))
	if err != nil || len(paths) != 1 || paths[0] != "/a" {
		t.Fatal(paths, err)
	}
}

func TestNativeEvidenceBeforeTranscriptAndWithoutQueueLease(t *testing.T) {
	c, _ := runtimeFixture(t)
	c.Enrichment = true
	root := filepath.Join(c.Home, ".claude")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	path := filepath.Join(root, "projects", "p", "s.jsonl")
	body, err := json.Marshal(map[string]any{"hook_event_name": "SessionStart", "transcript_path": path, "cwd": c.Home, "model": "sonnet", "prompt": "must never persist"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		code, err := runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "claude"}, strings.NewReader(string(body)))
		if err != nil || code != exitOK {
			t.Fatal(code, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(c.Spool, "native-evidence"))
	if err != nil || len(entries) != 2 {
		t.Fatal(len(entries), err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(c.Spool, "native-evidence", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "must never persist") {
			t.Fatal("prompt copied")
		}
		var record sessioncapture.NativeEvidence
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.Context.Event != "SessionStart" || record.Context.Model != "sonnet" {
			t.Fatal(record.Context)
		}
	}
}

func TestPiNativeProducerUsesSDKMetadataAndJSONStdin(t *testing.T) {
	c, config := runtimeFixture(t)
	exe := filepath.Join(c.Home, "synthetic-agent-sessions")
	if err := os.WriteFile(exe, []byte("synthetic executable fixture; never invoked"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(c.Home, ".pi", "agent")
	plan, err := capturehooks.Build(capturehooks.Options{Home: c.Home, Executable: exe, ConfigPath: config, Roots: []capturehooks.Root{{Agent: "pi", Path: root}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := capturehooks.Apply(t.Context(), plan, true); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "extensions", capturehooks.Marker+".ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{`source: event.reason`, `const file = ctx.sessionManager.getSessionFile();`, `if (!file) return; // --no-session has no durable source.`, `session_file: file`, `session_id: ctx.sessionManager.getSessionId()`, `cwd: ctx.cwd`, `model: ctx.model?.id`, `provider: ctx.model?.provider`, `argv: process.argv.slice(0, 32)`, `context_origin: "extension"`, `stdio: ["pipe", "ignore", "ignore"]`, `child.stdin.end(payload)`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing SDK producer fact %q", want)
		}
	}
	for _, forbidden := range []string{"getSystemPrompt", "getBranch(", "getEntries(", "process.env", "readFile"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("producer accesses forbidden context %q", forbidden)
		}
	}
}
