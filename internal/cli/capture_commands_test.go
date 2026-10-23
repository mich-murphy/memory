package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestCaptureCommandsDurableProgressAndPolicyRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_SESSIONS_DISABLE", "")
	root := t.TempDir()
	policy := filepath.Join(root, "policy")
	if err := os.MkdirAll(filepath.Join(policy, "exclusions"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.jsonl")
	content := []byte("{\"type\":\"user\",\"uuid\":\"synthetic\",\"message\":{\"role\":\"user\",\"content\":\"synthetic CLI input\"}}\n")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	spool, storeDir := filepath.Join(root, "spool"), filepath.Join(root, "store")
	common := []string{"--spool", spool, "--archive", "archive", "--machine", "machine", "--agent", "claude", "--session", "root/session", "--policy-dir", policy}
	invoke := func(want int, args ...string) []byte {
		t.Helper()
		var output, diagnostic bytes.Buffer
		code := Main(t.Context(), append([]string{"capture-v1"}, args...), "test", IO{Stdout: &output, Stderr: &diagnostic})
		if code != want {
			t.Fatalf("capture command %s exit=%d, want %d (private diagnostics withheld)", args[0], code, want)
		}
		return output.Bytes()
	}
	status := func(state string) {
		t.Helper()
		var rows []sessioncapture.Status
		if err := json.Unmarshal(invoke(0, "status", "--spool", spool, "--archive", "archive"), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].State != state || rows[0].Sequence != 1 {
			t.Fatalf("durable status must preserve accepted publication: rows=%d state=%s", len(rows), state)
		}
	}
	invoke(0, "init", "--store", storeDir, "--archive", "archive")
	invoke(0, append([]string{"capture"}, append(common, "--source", "main", "--path", source)...)...)
	status("pending")
	// Every CLI invocation reopens the durable queue; original bytes are no longer
	// needed after acceptance, but a subsequent opt-out must still prevent upload.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_DISABLE", "1")
	invoke(1, append([]string{"deliver"}, append(common, "--store", storeDir)...)...)
	status("policy-blocked")
	t.Setenv("AGENT_SESSIONS_DISABLE", "")
	invoke(0, append([]string{"deliver"}, append(common, "--store", storeDir)...)...)
	status("published")
	// An idempotent retry cannot create another job or require the deleted input.
	invoke(0, append([]string{"deliver"}, append(common, "--store", storeDir)...)...)
	status("published")
	destination := filepath.Join(root, "restored")
	invoke(0, "export", "--store", storeDir, "--archive", "archive", "--machine", "machine", "--agent", "claude", "--session", "root/session", "--output", destination)
	metadata, err := os.ReadFile(filepath.Join(destination, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resolved publication.Export
	if err := json.Unmarshal(metadata, &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved.Bodies) != 1 || resolved.Manifest.Sequence != 1 {
		t.Fatal("unexpected resolved publication coverage")
	}
	restored, err := os.ReadFile(filepath.Join(destination, resolved.Bodies[0].Path))
	if err != nil || !bytes.Equal(restored, content) {
		t.Fatalf("exact accepted bytes were not restored: %v", err)
	}
	// Failed argument/configuration attempts do not alter published progress.
	invoke(2, append([]string{"capture"}, append(common, "--source", "main", "--path", source, "--role", "unsupported")...)...)
	invoke(2, append([]string{"capture"}, common...)...)
	invoke(2, append([]string{"deliver"}, common...)...)
	invoke(1, "status", "--spool", spool, "--archive", "different-archive")
	invoke(1, "status", "--spool", spool, "--archive", "archive", "--max-spool-bytes", "0")
	status("published")
}
