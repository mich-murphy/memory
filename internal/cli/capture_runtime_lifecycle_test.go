package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestRuntimeWritableBindingMismatchAndStoppedOperations(t *testing.T) {
	c, config := runtimeFixture(t)
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	if err := publication.WriteArchive(t.Context(), publication.NewLocal(c.Store), publication.Archive{Contract: publication.Contract, ArchiveID: "other-archive"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runCaptureRuntime(t.Context(), "run", []string{"--config", config}, io); err == nil {
		t.Fatal("archive mismatch accepted")
	}
	if _, err := os.Stat(c.Spool); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("mismatch opened writable queue")
	}
	if _, err := runCaptureRuntime(t.Context(), "resume", []string{"--config", config}, io); err != nil {
		t.Fatal(err)
	}
	if _, err := runCaptureRuntime(t.Context(), "replay", []string{"--config", config, "--id", "999"}, io); err == nil {
		t.Fatal("missing quarantine replay succeeded")
	}
	if _, err := runCaptureRuntime(t.Context(), "enrichment-replay", []string{"--config", config, "--work-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, io); err == nil {
		t.Fatal("missing work replay succeeded")
	}
	for _, command := range []string{"work-status", "quarantine", "enrichment-status"} {
		out.Reset()
		if _, err := runCaptureRuntime(t.Context(), command, []string{"--config", config}, io); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal("inspection did not return JSON")
		}
	}
}

func TestRuntimeEnrichmentEnabledRunPublishesAndStatusDoesNotTakeLease(t *testing.T) {
	c, config := runtimeFixture(t)
	c.Enrichment = true
	c.OperationSeconds = 2
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Home, ".claude", "projects", "test", "main.jsonl")
	runtimeWrite(t, path)
	runtimeWrite(t, filepath.Join(c.Home, ".claude", "CLAUDE.md"))
	if err := publication.WriteArchive(t.Context(), publication.NewLocal(c.Store), publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := runCaptureRuntime(ctx, "run", []string{"--config", config}, IO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
		done <- err
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("coordinator did not join")
		}
	}()
	// The attachment budget covers serialized startup, not the 2s operation budget
	// alone. Under CPU and fsync contention (synchronous=FULL DDL) Open took up to
	// ~2.1s and InitRuntime up to ~2.4s, then discovery, capture, a second discovery
	// pass and the enrichment attempt added ~1s more: ~5.5s observed worst case with
	// 6 concurrent race+coverage copies. 30s is over 5x that and still far below the
	// 60s production operation default, so a real liveness defect still fails.
	deadline := time.Now().Add(30 * time.Second)
	for {
		rows, err := sessioncapture.ReadEnrichmentWorkStatus(t.Context(), c.Spool, c.ArchiveID, "", 64)
		attached := false
		for _, row := range rows {
			attached = attached || row.State == "attached"
		}
		if err == nil && attached {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatal("coordinator exited before enrichment")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("enrichment attachment deadline exceeded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var output bytes.Buffer
	if _, err := runCaptureRuntime(t.Context(), "enrichment-status", []string{"--config", config}, IO{Stdout: &output, Stderr: &output}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(output.Bytes()) {
		t.Fatal("live enrichment status invalid")
	}
}
