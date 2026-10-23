package cli

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/publicationr2"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

type captureFailingBackend struct{ err error }

func (b captureFailingBackend) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, b.err
}
func (b captureFailingBackend) PutFile(context.Context, string, string) error { return b.err }
func (b captureFailingBackend) Current(context.Context, string) ([]byte, string, error) {
	return nil, "", b.err
}

func (b captureFailingBackend) CompareAndSwap(context.Context, string, string, []byte) error {
	return b.err
}
func (b captureFailingBackend) Sync(context.Context, string) error { return b.err }

func TestCaptureDestinationMappingAndDurabilityForwarding(t *testing.T) {
	for _, cause := range []error{publicationr2.ErrDestinationPaused, publication.ErrDurability, context.Canceled, errors.New("synthetic-unavailable")} {
		b := &captureDestinationBackend{backend: captureFailingBackend{cause}, bound: true}
		calls := []func() error{func() error { _, e := b.Open(t.Context(), "key"); return e }, func() error { return b.PutFile(t.Context(), "key", "path") }, func() error { _, _, e := b.Current(t.Context(), "key"); return e }, func() error { return b.CompareAndSwap(t.Context(), "key", "", nil) }, func() error { return b.Sync(t.Context(), "key") }}
		for _, call := range calls {
			err := call()
			if !errors.Is(err, cause) {
				t.Fatal("backend error lost")
			}
			if errors.Is(err, sessioncapture.ErrDestination) != errors.Is(cause, publicationr2.ErrDestinationPaused) {
				t.Fatal("incorrect destination classification")
			}
		}
	}
}

func TestRuntimeRemoteArchiveGateBeforeNamespaceWrite(t *testing.T) {
	raw, err := captureFixtureBackend(t.Context(), t.TempDir(), "sdk-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := publication.WriteArchive(t.Context(), raw, publication.Archive{Contract: publication.Contract, ArchiveID: "actual"}); err != nil {
		t.Fatal(err)
	}
	b := &captureDestinationBackend{backend: raw, archive: "expected"}
	key := (publication.Identity{ArchiveID: "expected", MachineID: "machine", Agent: "claude", SessionID: "session"}).CurrentKey()
	if err := b.PutFile(t.Context(), key, "nonexistent"); !errors.Is(err, sessioncapture.ErrIntegrity) || !errors.Is(err, sessioncapture.ErrDestination) {
		t.Fatal("archive mismatch was not destination-fatal before opening upload source")
	}
	if _, _, err := raw.Current(t.Context(), key); !errors.Is(err, publication.ErrNotFound) {
		t.Fatal("mismatched archive received namespace bytes")
	}
	b.archive = "actual"
	if _, _, err := b.Current(t.Context(), key); !errors.Is(err, publication.ErrNotFound) {
		t.Fatal(err)
	}
	if err := b.Sync(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	bound := b.bound
	b.mu.Unlock()
	if !bound {
		t.Fatal("verified archive binding not retained")
	}
}
