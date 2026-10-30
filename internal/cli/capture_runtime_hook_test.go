package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestNativeCaptureHookUsesDiscoveryIdentityAndDurableInbox(t *testing.T) {
	c, _ := runtimeFixture(t)
	path := filepath.Join(c.Home, ".claude", "projects", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic raw tail"), 0o600); err != nil {
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
	payload, err := json.Marshal(map[string]string{"transcript_path": path})
	if err != nil {
		t.Fatal(err)
	}
	code, err := runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "claude"}, bytes.NewReader(payload))
	if err != nil || code != exitOK {
		t.Fatalf("native hook failed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(c.Spool, "hints"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("native discovery was not durably queued: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(c.Spool, "hints", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var request sessioncapture.Request
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	discover, closeDiscovery := runtimeDiscovery(c, filepath.Join(c.Home, "capture.json"))
	defer func() {
		if err := closeDiscovery(); err != nil {
			t.Error(err)
		}
	}()
	batch, _ := discover(t.Context()) // Explicit enrichment gaps do not erase raw requests.
	found := false
	for _, candidate := range batch.Requests {
		if candidate.Path == path {
			found = true
			if request.Identity != candidate.Identity || request.SourceID != candidate.SourceID || request.Role != candidate.Role {
				t.Fatal("hook/discovery identity disagreement")
			}
		}
	}
	if !found {
		t.Fatal("source not found by ordinary discovery")
	}
	status, err := q.Status(t.Context())
	if err != nil || len(status) != 0 {
		t.Fatal("a pathname hint was mislabeled captured bytes")
	}
}

func TestNativeCaptureInputIsBoundedAndContentFree(t *testing.T) {
	for _, input := range []string{"null", "{", `{"transcript_path":3,"private":"DO-NOT-ECHO"}`, strings.Repeat(" ", 64<<10) + `{"transcript_path":"private"}`} {
		_, err := nativeCapturePaths(t.Context(), strings.NewReader(input))
		if err == nil || strings.Contains(err.Error(), "DO-NOT-ECHO") {
			t.Fatalf("invalid input accepted or exposed: %v", err)
		}
	}
	paths, err := nativeCapturePaths(t.Context(), strings.NewReader(`{"transcript_path":"/synthetic","agent_transcript_path":"/synthetic","session_file":"/other"}`))
	if err != nil || len(paths) != 2 {
		t.Fatalf("native paths not deduplicated: %v", err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := nativeCapturePaths(ctx, reader); err == nil {
		t.Fatal("canceled input wait succeeded")
	}
}
