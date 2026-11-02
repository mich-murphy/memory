package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// The admission key-length rule restates capturebackend.ValidateIdentity; they must agree.
func TestAdmissionKeyLengthAgreesWithR2IdentityValidation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := sessioncapture.Open(t.Context(), filepath.Join(dir, "q"), sessioncapture.Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, sessioncapture.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	spec := capturebackend.Spec{Kind: "r2", Endpoint: "https://" + strings.Repeat("a", 32) + ".r2.cloudflarestorage.com", Bucket: "bucket-name", AccessKeyEnv: "AK", SecretKeyEnv: "SK"}
	want := map[int]bool{431: false, 432: false, 433: true, 434: true}
	for n, refused := range want {
		id := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: strings.Repeat("s", n)}
		herr := q.Hint(t.Context(), sessioncapture.Request{Identity: id, SourceID: "source", Role: "transcript", Path: p, ObservedAtNS: 1})
		verr := capturebackend.ValidateIdentity(spec, id)
		if errors.Is(herr, sessioncapture.ErrKeyLength) != refused || (verr != nil) != refused {
			t.Fatalf("session %d: hint=%v validate=%v want refused=%v", n, herr, verr, refused)
		}
	}
}
