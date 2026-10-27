package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

type captureArchiveBodyBackend struct {
	captureFailingBackend
	body string
}

func (b captureArchiveBodyBackend) Open(context.Context, string) (io.ReadCloser, error) {
	if b.err != nil {
		return nil, b.err
	}
	return io.NopCloser(strings.NewReader(b.body)), nil
}

func TestCaptureDestinationUnboundArchiveReadClassification(t *testing.T) {
	other := `{"contract":"` + publication.Contract + `","archive_id":"other"}`
	if raw, err := json.Marshal(publication.Archive{Contract: publication.Contract, ArchiveID: "other"}); err == nil {
		other = string(raw)
	}
	cases := []struct {
		name    string
		backend publication.Backend
		dest    bool
		is      error
	}{
		{"transport", captureFailingBackend{errors.New("publication destination operation failed")}, false, nil},
		{"auth", captureFailingBackend{publicationr2.ErrDestinationPaused}, true, publicationr2.ErrDestinationPaused},
		{"durability", captureFailingBackend{publication.ErrDurability}, false, publication.ErrDurability},
		{"canceled", captureFailingBackend{context.Canceled}, false, context.Canceled},
		{"deadline", captureFailingBackend{context.DeadlineExceeded}, false, context.DeadlineExceeded},
		{"not found", captureFailingBackend{publication.ErrNotFound}, true, publication.ErrNotFound},
		{"invalid archive", captureArchiveBodyBackend{body: `{"nope"`}, true, publication.ErrInvalidMetadata},
		{"unsupported archive", captureArchiveBodyBackend{body: `{"contract":"x","archive_id":"expected"}`}, true, publication.ErrInvalidMetadata},
		{"mismatch", captureArchiveBodyBackend{body: other}, true, sessioncapture.ErrIntegrity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &captureDestinationBackend{backend: tc.backend, archive: "expected"}
			_, _, err := b.Current(t.Context(), "capture-v1/sessions/x/current.json")
			if err == nil {
				t.Fatal("unbound read succeeded")
			}
			if got := errors.Is(err, sessioncapture.ErrDestination); got != tc.dest {
				t.Fatalf("destination classification %v, want %v", got, tc.dest)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatal("cause lost")
			}
			b.mu.Lock()
			bound := b.bound
			b.mu.Unlock()
			if bound {
				t.Fatal("failed archive read bound the destination")
			}
		})
	}
}

func TestTransientUnboundArchiveReadRetriesWithoutPause(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(p, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	q, err := sessioncapture.Open(t.Context(), filepath.Join(root, "queue"), sessioncapture.Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, sessioncapture.Request) error { return nil }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	id := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: "session"}
	if _, err = q.Capture(t.Context(), sessioncapture.Request{Identity: id, SourceID: "source", Role: "transcript", Path: p, ObservedAtNS: 1}); err != nil {
		t.Fatal(err)
	}
	b := &captureDestinationBackend{backend: captureFailingBackend{errors.New("publication destination operation failed")}, archive: "archive"}
	_ = q.DeliverDue(t.Context(), b, id)
	st, err := q.Status(t.Context())
	if err != nil || len(st) != 1 || st[0].State != "retry" || st[0].Attempts != 1 || st[0].NextRetryNS <= now.UnixNano() {
		t.Fatalf("%+v %v", st, err)
	}
	rs, err := q.RuntimeStatus(t.Context())
	if err != nil || rs.Paused {
		t.Fatalf("transient archive read paused destination: %+v %v", rs, err)
	}
	b = &captureDestinationBackend{backend: captureFailingBackend{publicationr2.ErrDestinationPaused}, archive: "archive"}
	if _, err = q.Capture(t.Context(), sessioncapture.Request{Identity: id, SourceID: "source", Role: "transcript", Path: p, ObservedAtNS: 2}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour) // past the recorded backoff
	_ = q.DeliverDue(t.Context(), b, id)
	rs, err = q.RuntimeStatus(t.Context())
	if err != nil || !rs.Paused || rs.PausedReason != "destination" {
		t.Fatalf("403 did not pause: %+v %v", rs, err)
	}
}
