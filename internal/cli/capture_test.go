package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestCaptureFixtureJourney(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "fixture")
	var out, diagnostic bytes.Buffer
	o := IO{Stdout: &out, Stderr: &diagnostic}
	if code := Main(t.Context(), []string{"capture-v1", "fixture", "--output", root}, "test", o); code != 0 {
		t.Fatalf("fixture failed (%d): %s", code, diagnostic.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "export", "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exported publication.Export
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Contract != publication.ReaderContract || len(exported.Bodies) != 3 || len(exported.Manifest.Sources) != 3 {
		t.Fatalf("incomplete retained epochs: contract=%s bodies=%d sources=%d", exported.Contract, len(exported.Bodies), len(exported.Manifest.Sources))
	}
	if exported.Manifest.Sequence != 4 || exported.Manifest.Coverage.Enrichment == "complete" {
		t.Fatal("unexpected ordering or false enrichment coverage")
	}
	// A second process-style export uses the same authoritative resolver, without
	// consulting the original sources or spool.
	if err := os.RemoveAll(filepath.Join(root, "sources")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "restored")
	id := exported.Manifest.Identity
	args := []string{"capture-v1", "export", "--store", filepath.Join(root, "store"), "--archive", id.ArchiveID, "--machine", id.MachineID, "--agent", id.Agent, "--session", id.SessionID, "--output", dest}
	if code := Main(t.Context(), args, "test", o); code != 0 {
		t.Fatalf("export failed (%d): %s", code, diagnostic.String())
	}
	for _, body := range exported.Bodies {
		want, err := os.ReadFile(filepath.Join(root, "export", body.Path))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dest, body.Path))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("restored epoch differs: %v", err)
		}
	}
}

func TestCaptureUnavailablePolicyFailsClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	policy := filepath.Join(root, "effective-policy")
	spool := filepath.Join(root, "spool")
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := publication.NewLocal(filepath.Join(root, "store"))
	id := publication.Identity{ArchiveID: "test", MachineID: "m", Agent: "claude", SessionID: "root/session"}
	if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: id.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	queue, err := sessioncapture.Open(t.Context(), spool, sessioncapture.Options{ArchiveID: id.ArchiveID, MaxSpoolBytes: 1 << 20, CheckPolicy: capturePolicy(policy, spool)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := queue.Close(); err != nil {
			t.Error(err)
		}
	})
	req := sessioncapture.Request{Identity: id, SourceID: "main", Role: "main", Path: path}
	if _, err := queue.Capture(t.Context(), req); !errors.Is(err, sessioncapture.ErrPolicy) {
		t.Fatalf("missing policy must reject capture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(policy, "exclusions"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_DISABLE", "1")
	if _, err := queue.Capture(t.Context(), req); !errors.Is(err, sessioncapture.ErrPolicy) {
		t.Fatalf("environment opt-out must reject capture: %v", err)
	}
	t.Setenv("AGENT_SESSIONS_DISABLE", "")
	if _, err := queue.Capture(t.Context(), req); err != nil {
		t.Fatalf("explicit initialized empty policy must work: %v", err)
	}
	if err := os.Rename(filepath.Join(policy, "exclusions"), filepath.Join(policy, "unavailable")); err != nil {
		t.Fatal(err)
	}
	if err := queue.Deliver(t.Context(), backend, id); !errors.Is(err, sessioncapture.ErrPolicy) {
		t.Fatalf("disappeared policy must reject delivery: %v", err)
	}
	if _, _, err := backend.Current(t.Context(), id.CurrentKey()); !errors.Is(err, publication.ErrNotFound) {
		t.Fatalf("forbidden publication became visible: %v", err)
	}
	status, err := queue.Status(t.Context())
	if err != nil || len(status) != 1 || status[0].State != "policy-blocked" {
		t.Fatalf("accepted bytes must remain pending and visible: %v", err)
	}
}

func TestCaptureRequiresExplicitLocalScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{
		{"capture-v1"},
		{"capture-v1", "init", "--archive", "a"},
		{"capture-v1", "capture", "--archive", "a", "--spool", filepath.Join(t.TempDir(), "queue")},
		{"capture-v1", "export", "--archive", "a"},
	} {
		var out bytes.Buffer
		code := Main(context.Background(), args, "test", IO{Stdout: &out, Stderr: &out})
		if code == 0 {
			t.Fatalf("unsafe invocation accepted: %v", args)
		}
	}
}
