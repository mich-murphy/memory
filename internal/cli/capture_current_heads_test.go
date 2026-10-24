package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
)

func TestCaptureCurrentHeadsRoutingHelpAndNoSpool(t *testing.T) {
	c, config := runtimeFixture(t)
	backend, err := captureBackend(t.Context(), c, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	// A file at the spool path makes accidental queue access fail, even if the
	// reader tried to create a queue after both machines' spools disappeared.
	if err := os.RemoveAll(c.Spool); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(c.Spool), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Spool, []byte("not a queue"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	for _, args := range [][]string{{"current-heads"}, {"current-heads", "--config", config, "--limit", "0"}, {"current-heads", "--config", config, "--limit", "257"}, {"current-heads", "--config", config, "extra"}} {
		if _, err := runCapture(t.Context(), args, io); err == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	code, err := runCapture(t.Context(), []string{"current-heads", "--config", config}, io)
	if code != exitFailed || !errors.Is(err, publication.ErrListingUnsupported) || out.Len() != 0 {
		t.Fatal("local enumeration must explicitly be unsupported", code, err, out.String())
	}
	c.Store = ""
	c.Backend = &capturebackend.Spec{Kind: "r2", Endpoint: "https://" + strings.Repeat("0", 32) + ".r2.cloudflarestorage.com", Bucket: "synthetic-bucket", AccessKeyEnv: "SYNTHETIC_HEADS_ACCESS", SecretKeyEnv: "SYNTHETIC_HEADS_SECRET"}
	t.Setenv(c.Backend.AccessKeyEnv, "")
	t.Setenv(c.Backend.SecretKeyEnv, "")
	if err = capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	code, err = runCapture(t.Context(), []string{"current-heads", "--config", config}, io)
	if code != exitFailed || err == nil || !strings.Contains(err.Error(), "credentials unavailable") || out.Len() != 0 {
		t.Fatal("remote reader did not require named backend auth", code, err)
	}
	f, err := parseRuntimeFlags("current-heads", []string{"--config", config}, io)
	if err != nil || f.limit != 256 || f.sessionAfter != "" {
		t.Fatal("incorrect defaults", err)
	}
	_, err = runCapture(t.Context(), nil, io)
	if err == nil || !strings.Contains(err.Error(), "current-heads") {
		t.Fatal("missing root help")
	}
	_, _ = runCapture(t.Context(), []string{"current-heads", "--help"}, io)
	for _, flag := range []string{"-config", "-after-session", "-limit"} {
		if !strings.Contains(out.String(), flag) {
			t.Fatal("missing help", flag)
		}
	}
}

type delegatedHeadBackend struct {
	publication.Backend
	called bool
	cause  error
}

func (b *delegatedHeadBackend) ListSessionNamespaces(_ context.Context, scope publication.Identity, after string, limit int) ([]string, bool, error) {
	b.called = scope.ArchiveID == "archive" && scope.MachineID == "machine" && scope.Agent == "pi" && after == "" && limit == 256
	return nil, true, b.cause
}

func TestCaptureDestinationDelegatesOptionalListing(t *testing.T) {
	inner := &delegatedHeadBackend{}
	b := &captureDestinationBackend{backend: inner, bound: true}
	_, done, err := b.ListSessionNamespaces(t.Context(), publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi"}, "", 256)
	if err != nil || !done || !inner.called {
		t.Fatal("listing not delegated", err)
	}
	b.backend = publication.NewLocal(t.TempDir())
	if _, _, err = b.ListSessionNamespaces(t.Context(), publication.Identity{}, "", 256); !errors.Is(err, publication.ErrListingUnsupported) {
		t.Fatal("unsupported capability lost", err)
	}
}
