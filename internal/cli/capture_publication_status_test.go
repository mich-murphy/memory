package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestCapturePublicationStatusLocalOnlyWhileOwnerActive(t *testing.T) {
	c, config := runtimeFixture(t)
	c.Store = ""
	c.Backend = &capturebackend.Spec{Kind: "r2", Endpoint: "https://" + strings.Repeat("0", 32) + ".r2.cloudflarestorage.com", Bucket: "synthetic-bucket", AccessKeyEnv: "SYNTHETIC_PUBLICATION_ACCESS", SecretKeyEnv: "SYNTHETIC_PUBLICATION_SECRET"}
	t.Setenv(c.Backend.AccessKeyEnv, "")
	t.Setenv(c.Backend.SecretKeyEnv, "")
	if err := capturedaemon.Save(config, c); err != nil {
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
	path := filepath.Join(c.Home, ".claude", "projects", "test", "session.jsonl")
	runtimeWrite(t, path)
	id := publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude", SessionID: "synthetic-session"}
	m, err := q.Capture(t.Context(), sessioncapture.Request{Identity: id, SourceID: "source", Role: "main", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	backend := publication.NewLocal(filepath.Join(filepath.Dir(c.Home), "local-backend"))
	if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), backend, id); err != nil {
		t.Fatal(err)
	}
	// Metadata inspection does not need the original source.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	io := IO{Stdout: &out, Stderr: &stderr}
	code, err := runCapture(t.Context(), []string{"publication-status", "--config", config, "--after-session", "", "--limit", "256"}, io)
	if code != exitOK || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	var page sessioncapture.PublicationHeads
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Contract != "session-publication-heads/v1" || page.ArchiveID != c.ArchiveID || len(page.Heads) != 1 || page.Heads[0].Identity != id || page.Heads[0].Sequence != m.Sequence || page.Heads[0].PublicationID != m.PublicationID || !page.Complete || page.NextCursor != id.BaseKey() {
		t.Fatalf("unexpected page: %+v", page)
	}
	for _, secret := range []string{c.Home, c.Spool, c.PolicyDir, "source", "synthetic\n", c.Backend.AccessKeyEnv, c.Backend.Endpoint} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("private information in output")
		}
	}
	for _, args := range [][]string{
		{"publication-status"},
		{"publication-status", "--config", config, "--limit", "0"},
		{"publication-status", "--config", config, "--limit", "257"},
		{"publication-status", "--config", config, "--after-session", "bad"},
		{"publication-status", "--config", config, "extra"},
	} {
		out.Reset()
		if _, err := runCapture(t.Context(), args, io); err == nil || out.Len() != 0 {
			t.Fatal("invalid status arguments succeeded or wrote output")
		}
	}
}

func TestCapturePublicationStatusHelpAndDefault(t *testing.T) {
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	f, err := parseRuntimeFlags("publication-status", []string{"--config", "synthetic"}, io)
	if err != nil || f.limit != 256 || f.sessionAfter != "" {
		t.Fatal("wrong publication page defaults", err)
	}
	if _, err := runCapture(t.Context(), nil, io); err == nil || !strings.Contains(err.Error(), "publication-status") {
		t.Fatal("root help missing publication-status")
	}
	out.Reset()
	_, _ = runCapture(t.Context(), []string{"publication-status", "--help"}, io)
	for _, expected := range []string{"--after-session", "--config", "--limit"} {
		// flag's help renderer uses single hyphens.
		if !strings.Contains(out.String(), strings.TrimPrefix(expected, "-")) {
			t.Fatal("missing publication-status flag help", expected)
		}
	}
}
