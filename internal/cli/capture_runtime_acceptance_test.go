package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturehooks"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestRuntimePreCancelledRunDoesNotCreateSpool(t *testing.T) {
	c, config := runtimeFixture(t)
	if err := publication.WriteArchive(t.Context(), publication.NewLocal(c.Store), publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out bytes.Buffer
	code, err := runCaptureRuntime(ctx, "run", []string{"--config", config}, IO{Stdout: &out, Stderr: &out})
	if err != nil || code != exitOK {
		t.Fatalf("pure startup cancellation was not graceful: %v", err)
	}
	if _, err := os.Stat(c.Spool); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled archive validation opened a queue")
	}
}

func TestRuntimeOwnedHooksLifecycleAndPrivatePlan(t *testing.T) {
	c, config := runtimeFixture(t)
	executable := filepath.Join(c.Home, "candidate")
	if err := os.WriteFile(executable, []byte("synthetic executable fixture\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(c.Home, ".claude", "settings.json")
	before := []byte(`{"env":{"TOKEN":"synthetic-private-hook-setting"},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"agent-sessions hook claude"}]}]}}`)
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, before, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(action string, apply bool) error {
		t.Helper()
		args := []string{"--config", config, "--executable", executable, "--action", action}
		if apply {
			args = append(args, "--apply")
		}
		var out bytes.Buffer
		_, err := runCaptureRuntime(t.Context(), "hooks", args, IO{Stdout: &out, Stderr: &out})
		if strings.Contains(out.String(), "synthetic-private-hook-setting") {
			t.Fatal("public plan exposed existing settings")
		}
		return err
	}
	for _, action := range []string{"plan", "install", "uninstall"} {
		if err := run(action, false); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(settings)
		if err != nil || !bytes.Equal(body, before) {
			t.Fatal("dry-run changed settings")
		}
	}
	for _, action := range []string{"restart", "stop", "unknown"} {
		if run(action, false) == nil {
			t.Fatal("unsupported hooks action accepted")
		}
	}
	if run("plan", true) == nil {
		t.Fatal("applying a plan was accepted")
	}
	if err := run("install", true); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(settings)
	if err != nil || !bytes.Contains(body, []byte(capturehooks.Marker)) || !bytes.Contains(body, []byte("agent-sessions hook claude")) {
		t.Fatal("candidate installation lost owned or legacy hooks")
	}
	if err := run("uninstall", true); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var original, restored any
	if err := json.Unmarshal(before, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, restored) {
		t.Fatal("uninstall changed unrelated settings or legacy hooks")
	}
}

func TestRuntimeIndexedExternalPrivacySurvivesOwnerDeletion(t *testing.T) {
	c, _ := runtimeFixture(t)
	main := filepath.Join(c.Home, ".claude", "projects", "p", "12345678-1234-1234-1234-123456789abc.jsonl")
	runtimeWrite(t, main)
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	opts := capturesource.Options{
		ArchiveID: c.ArchiveID, MachineID: c.MachineID, Home: c.Home, Derived: true,
		LookupOwner: func(ctx context.Context, root capturesource.Root, uuid string) (string, string, error) {
			return sessioncapture.ReadOwnership(ctx, c.Spool, c.ArchiveID, root.Agent, root.Path, uuid)
		}, Policy: func(ctx context.Context, scope capturesource.Scope) error {
			return runtimeDiscoveryPolicy(c)(ctx, sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: scope.Root.Agent}, Path: scope.Path})
		},
	}
	request, err := capturesource.Classify(t.Context(), opts, "claude", main)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Hint(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(c.Home, ".claude", "file-history", "12345678-1234-1234-1234-123456789abc", "revision.txt")
	runtimeWrite(t, artifact)
	r, err := capturesource.Classify(t.Context(), opts, "claude", artifact)
	if err != nil || r.Identity != request.Identity {
		t.Fatalf("bounded ownership was not shared with capture: %v", err)
	}
	if _, err := q.Capture(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := exclusion.RecordPath(c.PolicyDir, adapters.NewClaude(c.Home), main); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(main); err != nil {
		t.Fatal(err)
	}
	r.Identity.SessionID = "forged-unexcluded-session"
	if runtimePolicy(c)(t.Context(), r) == nil {
		t.Fatal("request identity overrode indexed artifact privacy")
	}
	backend := publication.NewLocal(c.Store)
	if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), backend, request.Identity); !errors.Is(err, sessioncapture.ErrPolicy) {
		t.Fatal("pending artifact bypassed its missing opted-out owner's identity")
	}
}
