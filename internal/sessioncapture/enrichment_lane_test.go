package sessioncapture

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func snapshotOptions(body string) RuntimeOptions {
	return runtimeDefaults(RuntimeOptions{Enrich: func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{Body: io.NopCloser(strings.NewReader(body)), Complete: true}, nil
	}})
}

func enrichOnce(t *testing.T, q *Queue, o RuntimeOptions) {
	t.Helper()
	did, err := q.enrichmentNext(t.Context(), o)
	if err != nil || !did {
		t.Fatalf("work %v: %v", did, err)
	}
}

func TestEnrichmentStalledSameSessionRawPublishesAndStaleBasis(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	first := capture(t, q, r)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	opts := snapshotOptions("bundle")
	opts.Enrich = func(ctx context.Context, _ EnrichmentSpec) (EnrichmentArtifact, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return EnrichmentArtifact{}, ctx.Err()
		}
		return EnrichmentArtifact{Body: io.NopCloser(strings.NewReader("bundle")), Complete: true}, nil
	}
	go func() { _, err := q.enrichmentNext(t.Context(), opts); done <- err }()
	<-started
	write(t, r.Path, "first\nnewer\n")
	newer := capture(t, q, r)
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	remote, err := publication.Read(t.Context(), backend, r.Identity)
	if err != nil || remote.PublicationID != newer.PublicationID {
		t.Fatalf("raw stalled: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	latest, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Sources[0].Revision != newer.Sources[0].Revision || latest.Coverage.Enrichment != "partial" {
		t.Fatal("stale artifact greened/regressed raw")
	}
	result, err := q.loadEnrichmentResult(t.Context(), es[1].ResultID)
	if err != nil || result.Spec.BasisPublication != first.PublicationID {
		t.Fatalf("lost basis: %v", err)
	}
	if latest.CapturedAtNS != newer.CapturedAtNS {
		t.Fatal("attachment invented primary freshness")
	}
}

func TestEquivalentSnapshotsReuseStreamEpochAndCaptureTime(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	capture(t, q, r)
	opts := snapshotOptions("equivalent")
	enrichOnce(t, q, opts)
	before, _, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	artifact := before.Sources[1]
	write(t, r.Path, "first\nsecond\n")
	raw := capture(t, q, r)
	enrichOnce(t, q, opts)
	after, _, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if after.PublicationID != raw.PublicationID || len(after.Sources) != 2 || !reflect.DeepEqual(after.Sources[1], artifact) {
		t.Fatal("equivalent snapshot manufactured publication/epoch/time")
	}
	write(t, r.Path, "rewrite")
	capture(t, q, r)
	enrichOnce(t, q, snapshotOptions("changed"))
	changed, _, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Sources) != 4 || changed.Sources[1].State != "sealed" || changed.Sources[3].SourceID != artifact.SourceID || changed.Sources[3].EpochID == artifact.EpochID {
		t.Fatal("changed snapshot lost retained stream epoch")
	}
}

func TestRawAcceptanceNeverChecksOptionalPolicyAndFullWorkAdmission(t *testing.T) {
	dir, o, r := setup(t)
	var deny atomic.Bool
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if req.Optional && deny.Load() {
			return errors.New("optional callback must not run")
		}
		return nil
	}
	o.EnableEnrichment = true
	q := open(t, dir, o)
	capture(t, q, r)
	enrichOnce(t, q, snapshotOptions("bundle"))
	deny.Store(true)
	// Exhaust the explicit enrichment metadata budget without touching primary
	// payload space. A new raw job retains its admission gap and remains deliverable.
	if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=used WHERE name='enrichment'`); err != nil {
		t.Fatal(err)
	}
	write(t, r.Path, "first\nappend\n")
	m := capture(t, q, r)
	var gap string
	if err := q.db.QueryRowContext(t.Context(), `SELECT enrichment_gap FROM jobs WHERE sequence=?`, m.Sequence).Scan(&gap); err != nil || gap != "quota" {
		t.Fatalf("gap %q: %v", gap, err)
	}
	backend := newBackend(t)
	for range 3 {
		if err := q.DeliverDue(t.Context(), backend, r.Identity); err != nil {
			t.Fatal(err)
		}
	}
	remote, err := publication.Read(t.Context(), backend, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if remote.Sources[0].Object.SHA256 != m.Sources[0].Object.SHA256 || remote.Sources[1].Withheld == nil {
		t.Fatal("optional denial blocked latest raw")
	}
}

func TestEnrichmentPolicyRevocationBoundariesAndRecoveryLatest(t *testing.T) {
	for _, boundary := range []string{"upload-before", "manifest-upload-before", "current-before"} {
		t.Run(boundary, func(t *testing.T) {
			dir, o, r := setup(t)
			var denied bool
			armed := false
			o.CheckPolicy = func(_ context.Context, req Request) error {
				if req.Optional && denied {
					return ErrPolicy
				}
				return nil
			}
			o.Fault = func(stage string) error {
				if armed && stage == boundary {
					denied = true
					armed = false
				}
				return nil
			}
			o.EnableEnrichment = true
			q := open(t, dir, o)
			capture(t, q, r)
			enrichOnce(t, q, snapshotOptions("bundle"))
			write(t, r.Path, "first\nlatest\n")
			raw := capture(t, q, r)
			armed = true
			backend := newBackend(t)
			for range 5 {
				if err := q.DeliverDue(t.Context(), backend, r.Identity); err != nil {
					t.Fatal(err)
				}
			}
			remote, err := publication.Read(t.Context(), backend, r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if remote.Sources[0].Object != raw.Sources[0].Object || remote.Sources[1].Withheld == nil {
				t.Fatal("latest safe raw not published")
			}
			denied = false
			changed, err := q.RefreshOptionalSelection(t.Context(), r.Identity)
			if err != nil || !changed {
				t.Fatalf("recovery %v: %v", changed, err)
			}
			for range 3 {
				if err := q.DeliverDue(t.Context(), backend, r.Identity); err != nil {
					t.Fatal(err)
				}
			}
			restored, err := publication.Read(t.Context(), backend, r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Sources[0].Object != raw.Sources[0].Object || restored.Sources[1].Withheld != nil || restored.Sources[1].CapturedAtNS != remote.Sources[1].CapturedAtNS {
				t.Fatal("recovery regressed/latest or faked time")
			}
			status, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			superseded := 0
			for _, s := range status {
				if s.State == "superseded" {
					superseded++
				}
			}
			if superseded == 0 {
				t.Fatal("blocked jobs falsely published")
			}
		})
	}
}

func TestEnrichmentBusyAttachmentRetainsResultWithoutPoison(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	capture(t, q, r)
	opts := snapshotOptions("bundle")
	lock := q.session(r.Identity)
	lock.Lock()
	enrichOnce(t, q, opts)
	lock.Unlock()
	status, err := q.EnrichmentWorkStatus(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].State != "ready" || status[0].Attempts != 0 {
		t.Fatalf("busy poisoned: %+v", status)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE enrichment_work SET next_retry=0`); err != nil {
		t.Fatal(err)
	}
	enrichOnce(t, q, opts)
	if err := q.ReplayEnrichment(t.Context(), status[0].ID); err != nil {
		t.Fatal(err)
	}
	opts.Enrich = func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		t.Error("replay rebuilt owned result")
		return EnrichmentArtifact{}, ErrIntegrity
	}
	enrichOnce(t, q, opts)
}

type failingArtifactBody struct{ io.Reader }

func (f failingArtifactBody) Close() error { return errors.New("close failed") }
func TestEnrichmentCloseAndCancellationRemainFailures(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	capture(t, q, r)
	opts := snapshotOptions("")
	opts.Enrich = func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{Body: failingArtifactBody{strings.NewReader("owned")}}, nil
	}
	enrichOnce(t, q, opts)
	status, err := q.EnrichmentWorkStatus(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if status[0].State != "retry" {
		t.Fatal("close failure hidden")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.buildEnrichment(ctx, EnrichmentSpec{Identity: r.Identity}, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
}
