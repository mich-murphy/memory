package sessioncapture

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

type optionalBackend struct {
	publication.Backend
	key     string
	stage   string
	fail    atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    atomic.Bool
}

func (b *optionalBackend) boundary(ctx context.Context, stage, key string) error {
	if key != b.key || stage != b.stage {
		return nil
	}
	if b.entered != nil && b.once.CompareAndSwap(false, true) {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if b.fail.Load() {
		return errors.New("private remote failure")
	}
	return nil
}

func (b *optionalBackend) PutFile(ctx context.Context, key, path string) error {
	if err := b.boundary(ctx, "upload", key); err != nil {
		return err
	}
	return b.Backend.PutFile(ctx, key, path)
}

func (b *optionalBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := b.boundary(ctx, "verify", key); err != nil {
		return nil, err
	}
	return b.Backend.Open(ctx, key)
}

func (b *optionalBackend) Sync(ctx context.Context, key string) error {
	if err := b.boundary(ctx, "object-fence", key); err != nil {
		return err
	}
	if durable, ok := b.Backend.(publication.DurabilityFence); ok {
		return durable.Sync(ctx, key)
	}
	return nil
}

func optionalFixture(t *testing.T) (*Queue, Request, publication.Manifest) {
	t.Helper()
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	capture(t, q, r)
	enrichOnce(t, q, snapshotOptions("optional body"))
	m, _, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	return q, r, m
}

func TestOptionalNetworkStallDoesNotLockRawAcceptanceOrClaims(t *testing.T) {
	for _, stage := range []string{"upload", "verify", "object-fence"} {
		t.Run(stage, func(t *testing.T) {
			q, r, m := optionalFixture(t)
			backend := &optionalBackend{Backend: newBackend(t), key: m.Sources[1].Object.Key, stage: stage, entered: make(chan struct{}), release: make(chan struct{})}
			if stage == "object-fence" {
				// Reconciliation fences an already-visible publication.
				backend.entered = nil
				if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
					t.Fatal(err)
				}
				if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state='pending' WHERE sequence=?`, m.Sequence); err != nil {
					t.Fatal(err)
				}
				backend.entered = make(chan struct{})
			}
			done := make(chan error, 1)
			go func() { done <- q.Deliver(t.Context(), backend, r.Identity) }()
			select {
			case <-backend.entered:
			case err := <-done:
				t.Fatalf("optional delivery returned before %s boundary: %v", stage, err)
			case <-time.After(2 * time.Second):
				t.Fatal("optional operation never started")
			}
			write(t, r.Path, "first\nnewest\n")
			captured := make(chan publication.Manifest, 1)
			captureErr := make(chan error, 1)
			go func() {
				next, err := q.Capture(t.Context(), r)
				if err != nil {
					captureErr <- err
				} else {
					captured <- next
				}
			}()
			select {
			case <-captured:
			case err := <-captureErr:
				t.Fatal(err)
			case <-time.After(2 * time.Second):
				t.Fatal("remote optional operation locked raw capture")
			}
			if err := q.Hint(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			if _, err := q.db.ExecContext(t.Context(), `UPDATE runtime_sessions SET claim=1`); err != nil {
				t.Fatal(err)
			}
			work, err := q.claimSource(t.Context())
			if err != nil || work.id == 0 {
				t.Fatalf("delivery claim blocked source claim: %v", err)
			}
			// The old optional operation remains blocked throughout reader and
			// local ACK assertions. Capture completion alone is not isolation.
			published := make(chan error, 1)
			go func() { published <- q.Deliver(t.Context(), backend, r.Identity) }()
			select {
			case err := <-published:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("optional network blocked safe raw publication")
			}
			dest := filepath.Join(t.TempDir(), "export")
			exported, err := publication.ExportSession(t.Context(), backend, r.Identity, dest)
			if err != nil {
				t.Fatal(err)
			}
			if exported.Manifest.Sources[1].Withheld == nil || len(exported.Bodies) != 1 {
				t.Fatal("new raw view did not withhold stalled optional")
			}
			body, err := os.ReadFile(filepath.Join(dest, exported.Bodies[0].Path))
			if err != nil || string(body) != "first\nnewest\n" {
				t.Fatalf("reader body %q: %v", body, err)
			}
			var state string
			if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE sequence=?`, exported.Manifest.Sequence).Scan(&state); err != nil || state != "published" {
				t.Fatalf("safe raw ACK %q: %v", state, err)
			}
			if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE sequence=?`, m.Sequence).Scan(&state); err != nil || state != "superseded" {
				t.Fatalf("old composite %q: %v", state, err)
			}
			close(backend.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			after, err := publication.Read(t.Context(), backend, r.Identity)
			if err != nil || after.PublicationID != exported.Manifest.PublicationID {
				t.Fatal("old CAS overwrote newer raw", err)
			}
		})
	}
}

func TestOptionalRemoteFailureWithholdsLatestAndRepairsAfterRestart(t *testing.T) {
	for _, stage := range []string{"upload", "verify", "object-fence"} {
		t.Run(stage, func(t *testing.T) {
			dir, o, r := setup(t)
			o.EnableEnrichment = true
			q, err := Open(t.Context(), dir, o)
			if err != nil {
				t.Fatal(err)
			}
			capture(t, q, r)
			enrichOnce(t, q, snapshotOptions("optional"))
			m, _, err := q.latest(t.Context(), r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			backend := &optionalBackend{Backend: newBackend(t), key: m.Sources[1].Object.Key, stage: stage}
			backend.fail.Store(true)
			// object-fence is exercised on exact-publication lost-ack reconciliation.
			if stage == "object-fence" {
				backend.fail.Store(false)
				if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
					t.Fatal(err)
				}
				if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state='pending' WHERE sequence=?`, m.Sequence); err != nil {
					t.Fatal(err)
				}
				backend.fail.Store(true)
			}
			write(t, r.Path, "first\nlatest\n")
			raw := capture(t, q, r)
			for range 5 {
				if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
					t.Fatal(err)
				}
			}
			remote, err := publication.Read(t.Context(), backend, r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if remote.Sources[0].Object != raw.Sources[0].Object || remote.Sources[1].Withheld == nil || remote.Sources[1].Withheld.Reason != "unavailable" {
				t.Fatal("raw latest was blocked or optional not redacted")
			}
			if _, err := q.RefreshOptionalSelection(t.Context(), r.Identity); err != nil {
				t.Fatal(err)
			}
			local, _, err := q.latest(t.Context(), r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if local.Sources[1].Withheld == nil {
				t.Fatal("local bytes cleared remote failure latch")
			}
			used := q.used
			if err := q.Close(); err != nil {
				t.Fatal(err)
			}
			q, err = Open(t.Context(), dir, o)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := q.Close(); err != nil {
					t.Error(err)
				}
			}()
			if q.used != used {
				t.Fatal("retained failed payload uncharged after restart")
			}
			backend.fail.Store(false)
			did, err := q.optionalRepairNext(t.Context(), backend, runtimeDefaults(RuntimeOptions{}))
			if err != nil || !did {
				t.Fatalf("repair %v %v", did, err)
			}
			for range 3 {
				if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
					t.Fatal(err)
				}
			}
			repaired, err := publication.Read(t.Context(), backend, r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if repaired.Sources[1].Withheld != nil || repaired.Sources[0].Object != raw.Sources[0].Object || repaired.Sources[1].CapturedAtNS != remote.Sources[1].CapturedAtNS {
				t.Fatal("repair restored stale main/fake capture time")
			}
			var attempts int
			if err := q.db.QueryRowContext(t.Context(), `SELECT attempts FROM optional_repairs`).Scan(&attempts); err != nil || attempts == 0 {
				t.Fatal("row-level attempts lost", attempts, err)
			}
		})
	}
}

func TestRequiredRemoteFailureNotWithheldAndCancelNotSuccess(t *testing.T) {
	q, r, m := optionalFixture(t)
	backend := &optionalBackend{Backend: newBackend(t), key: m.Sources[0].Object.Key, stage: "upload"}
	backend.fail.Store(true)
	if err := q.Deliver(t.Context(), backend, r.Identity); err == nil {
		t.Fatal("required failure accepted")
	}
	latest, _, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Sequence != m.Sequence {
		t.Fatal("required failure manufactured selection")
	}
	var repairs int
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM optional_repairs`).Scan(&repairs); err != nil || repairs != 0 {
		t.Fatal("required failure became optional repair", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := q.Deliver(ctx, backend, r.Identity); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWorkingDirectoryIsTransientAndReserved(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	spec := EnrichmentSpec{Kind: "snapshot", Identity: r.Identity, WorkingDirectory: "/untrusted"}
	options := snapshotOptions("body")
	options.Enrich = func(_ context.Context, s EnrichmentSpec) (EnrichmentArtifact, error) {
		if s.WorkingDirectory == "" || s.WorkingDirectory == spec.WorkingDirectory || !strings.HasPrefix(s.WorkingDirectory, dir) {
			t.Error("callback scratch not reserved")
		}
		return EnrichmentArtifact{Body: io.NopCloser(strings.NewReader("body"))}, nil
	}
	if _, err := q.buildEnrichment(t.Context(), spec, options); err != nil {
		t.Fatal(err)
	}
	if digestSpec(spec) != digestSpec(EnrichmentSpec{Kind: spec.Kind, Identity: spec.Identity}) {
		t.Fatal("scratch directory entered durable identity")
	}
}
