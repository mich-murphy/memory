package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func runtimeFixture(t *testing.T) (capturedaemon.Config, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := capturedaemon.Config{Version: capturedaemon.ConfigVersion, ArchiveID: "runtime-test", MachineID: "test-machine", Store: filepath.Join(root, "store"), Spool: filepath.Join(root, "spool"), PolicyDir: filepath.Join(root, "policy"), Home: filepath.Join(root, "home"), DefaultRoots: true, CaptureWorkers: 1, DeliveryWorkers: 1, PollSeconds: 1, OperationSeconds: 1, MaxAttempts: 2, MaxSpoolBytes: 1 << 20, EntriesPerRoot: 16}
	if err = os.MkdirAll(filepath.Join(c.PolicyDir, "exclusions"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(c.Home, "capture.json")
	if err = capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	return c, config
}

func TestRuntimeHintBypassesQueueLockAndPolicyReloads(t *testing.T) {
	c, config := runtimeFixture(t)
	path := filepath.Join(c.Home, ".claude", "projects", "test", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic\n"), 0o600); err != nil {
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
	var out bytes.Buffer
	o := IO{Stdout: &out, Stderr: &out}
	args := []string{"--config", config, "--agent", "claude", "--session", "test-session", "--source", "test-source", "--path", path}
	if _, err = runCaptureRuntime(t.Context(), "hint", args, o); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(c.Spool, "hints"))
	if err != nil || len(entries) != 1 {
		t.Fatal("hint not durably queued")
	}
	if err = os.Remove(filepath.Join(c.PolicyDir, "exclusions")); err != nil {
		t.Fatal(err)
	}
	if _, err = runCaptureRuntime(t.Context(), "hint", args, o); err == nil {
		t.Fatal("unavailable policy accepted")
	}
}

func TestRuntimeRunValidatesArchiveAndJoinsCancellation(t *testing.T) {
	c, config := runtimeFixture(t)
	var out bytes.Buffer
	o := IO{Stdout: &out, Stderr: &out}
	if _, err := runCaptureRuntime(t.Context(), "run", []string{"--config", config}, o); err == nil {
		t.Fatal("missing archive accepted")
	}
	if _, err := os.Stat(c.Spool); !os.IsNotExist(err) {
		t.Fatal("queue created before archive validation")
	}
	if err := publication.WriteArchive(t.Context(), publication.NewLocal(c.Store), publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := runCaptureRuntime(ctx, "run", []string{"--config", config}, o); done <- err }()
	startup := time.Now().Add(5 * time.Second)
	for {
		if _, err := sessioncapture.ReadRuntime(t.Context(), c.Spool, c.ArchiveID, 0, 1); err == nil {
			break
		}
		if time.Now().After(startup) {
			cancel()
			<-done
			t.Fatal("runtime did not start")
		}
		select {
		case err := <-done:
			t.Fatalf("runtime exited before it was running: %v", err)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime failed to join")
	}
}

func TestRuntimeParserAndServiceDryRun(t *testing.T) {
	c, config := runtimeFixture(t)
	var out bytes.Buffer
	o := IO{Stdout: &out, Stderr: &out}
	// Routing is not covered elsewhere for every command (mutation-checked: dropping "hint" passed without this).
	for _, cmd := range []string{"run", "hint", "work-status", "quarantine", "replay", "resume", "service"} {
		if !runtimeCommand(cmd) {
			t.Fatalf("%s not routed", cmd)
		}
	}
	if _, err := parseRuntimeFlags("run", nil, o); err == nil {
		t.Fatal("missing config accepted")
	}
	if _, err := runCaptureRuntime(t.Context(), "service", []string{"--config", config, "--platform", "linux", "--executable", filepath.Join(c.Home, "synthetic binary %")}, o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Home, ".config")); !os.IsNotExist(err) {
		t.Fatal("service dry-run changed home")
	}
	if out.Len() == 0 {
		t.Fatal("missing service plan")
	}
	if _, err := runCaptureRuntime(t.Context(), "hint", []string{"--config", config}, o); err == nil {
		t.Fatal("implicit hint accepted")
	}
}

func TestRuntimeQueueInventoryQuotaConfiguration(t *testing.T) {
	c, _ := runtimeFixture(t)
	c.MaxInventoryRecords = 32768
	opts, err := runtimeQueueOptions(c)
	if err != nil || opts.MaxInventoryRecords != 32768 {
		t.Fatalf("quota not forwarded: %+v %v", opts, err)
	}
}
