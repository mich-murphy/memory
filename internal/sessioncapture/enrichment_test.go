package sessioncapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestNativeEvidenceBeforeSourceAndRetry(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "spool")
	identity := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "claude", SessionID: "session"}
	record := NativeEvidence{Contract: EnrichmentContract, ID: "launch-a", Identity: identity, Request: Request{Identity: identity, SourceID: "source", Role: "main", Path: filepath.Join(t.TempDir(), "absent")}, ObservedAtNS: 1, Context: NativeContext{Event: "start", ContextOrigin: "native"}}
	if err := WriteNativeEvidence(ctx, dir, record); err != nil {
		t.Fatal(err)
	}
	if err := WriteNativeEvidence(ctx, dir, record); err != nil {
		t.Fatal(err)
	}
	record.ID = "launch-b"
	if err := WriteNativeEvidence(ctx, dir, record); err != nil {
		t.Fatal(err)
	}
	q, err := Open(ctx, dir, Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.drainNativeEvidence(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.AcceptNativeEvidence(ctx, record); err != nil {
		t.Fatal(err)
	}
	record.Context.Model = "changed"
	if err := q.AcceptNativeEvidence(ctx, record); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("conflict: %v", err)
	}
	var count int
	if err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM enrichment_work`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("work %d: %v", count, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = Open(ctx, dir, Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_evidence`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("retained %d: %v", count, err)
	}
	record.Context.Argv = []string{"--prompt", "secret"}
	if err := WriteNativeEvidence(ctx, dir, record); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalRevocationDoesNotGateRaw(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	main := filepath.Join(root, "main.jsonl")
	artifact := filepath.Join(root, "artifact")
	for _, p := range []string{main, artifact} {
		if err := os.WriteFile(p, []byte("first"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	denied := false
	q, err := Open(ctx, filepath.Join(t.TempDir(), "spool"), Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, Now: time.Now, CheckPolicy: func(_ context.Context, r Request) error {
		if denied && r.Optional {
			return ErrPolicy
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	identity := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "claude", SessionID: "session"}
	raw := Request{Identity: identity, SourceID: "main", Role: "main", Path: main}
	if _, err := q.Capture(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Capture(ctx, Request{Identity: identity, SourceID: "artifact", Role: "artifact", Path: artifact, Optional: true}); err != nil {
		t.Fatal(err)
	}
	denied = true
	if err := os.WriteFile(main, []byte("firstsecond"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := q.Capture(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.RefreshOptionalSelection(ctx, identity); err != nil {
		t.Fatal(err)
	}
	m, _, err = q.latest(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if m.Sources[1].Withheld == nil || m.Sources[1].SourceID != "" || m.Sources[1].Path != "" {
		t.Fatal("optional not redacted")
	}
	_, es, err := q.latest(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if es[1].Original == nil || es[1].Original.SourceID != "artifact" {
		t.Fatal("original lost")
	}
	unchanged, err := q.CaptureIfChanged(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := publication.ValidateManifest(unchanged); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(m)
	b, _ := json.Marshal(unchanged)
	if !bytes.Equal(a, b) {
		t.Fatal("unchanged view changed")
	}
}
