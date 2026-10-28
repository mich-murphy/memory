package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestConfiguredLocalInitializerAndAuthoritativeExport(t *testing.T) {
	c, config := runtimeFixture(t)
	c.Backend = &capturebackend.Spec{Kind: "local", LocalDir: c.Store}
	c.Store = ""
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	if _, err := runCapture(t.Context(), []string{"init", "--config", config}, io); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Home, ".claude", "projects", "test", "a.jsonl")
	runtimeWrite(t, path)
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	})
	id := publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude", SessionID: "synthetic-session"}
	if _, err := q.Capture(t.Context(), sessioncapture.Request{Identity: id, SourceID: "main", Role: "main", Path: path}); err != nil {
		t.Fatal(err)
	}
	backend, err := capturebackend.Open(t.Context(), c.BackendSpec(), capturebackend.Dependencies{LookupEnv: func(string) (string, bool) { t.Fatal("local mode read credentials"); return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), backend, id); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(filepath.Dir(c.Home), "configured-export")
	if _, err := runCapture(t.Context(), []string{"export", "--config", config, "--agent", "claude", "--session", id.SessionID, "--output", destination}, io); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--config", config, "--store", filepath.Dir(c.Home)}, {"export", "--config", config, "--archive", "other", "--output", destination}, {"status", "--config", config}, {"export", "--config", config}} {
		if _, err := runCapture(t.Context(), args, io); err == nil {
			t.Fatal("conflicting/incomplete configured operation succeeded")
		}
	}
}

func TestRuntimeRemoteInspectionAndReplayNeverRequireCredentials(t *testing.T) {
	c, config := runtimeFixture(t)
	c.Store = ""
	c.Backend = &capturebackend.Spec{Kind: "r2", Endpoint: "https://" + strings.Repeat("0", 32) + ".r2.cloudflarestorage.com", Bucket: "synthetic-bucket", AccessKeyEnv: "SYNTHETIC_CAPTURE_ACCESS", SecretKeyEnv: "SYNTHETIC_CAPTURE_SECRET"}
	t.Setenv(c.Backend.AccessKeyEnv, "")
	t.Setenv(c.Backend.SecretKeyEnv, "")
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	if _, err := runCaptureRuntime(t.Context(), "resume", []string{"--config", config}, io); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"work-status", "quarantine", "enrichment-status", "policy-pauses"} {
		if _, err := runCaptureRuntime(t.Context(), cmd, []string{"--config", config}, io); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureBackend(t.Context(), c, false); err == nil || !strings.Contains(err.Error(), "credentials unavailable") {
		t.Fatal("explicit backend operation did not fail before network without credentials")
	}
	path := filepath.Join(c.Home, ".claude", "projects", "test", "a.jsonl")
	runtimeWrite(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := runCaptureRuntime(ctx, "run", []string{"--config", config}, io); done <- err }()
	t.Cleanup(cancel)
	for {
		view, err := sessioncapture.ReadRuntime(t.Context(), c.Spool, c.ArchiveID, 0, 256)
		if err == nil && len(view.Sources) > 0 && view.Sources[0].CapturedAtNS > 0 && view.Runtime.Paused {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("runtime exited before raw capture and paused delivery without credentials: %v", err)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	id := publication.Identity{ArchiveID: strings.Repeat("x", 256), MachineID: strings.Repeat("y", 256), Agent: "claude", SessionID: strings.Repeat("z", 256)}
	c.ArchiveID, c.MachineID = id.ArchiveID, id.MachineID
	if err := runtimePolicy(c)(t.Context(), sessioncapture.Request{Identity: id, Path: path, Role: "main"}); err == nil {
		t.Fatal("unsupported remote logical identity admitted")
	}
}
