package sessioncapture

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

type dedupeFixture struct {
	q    *Queue
	main Request
	sub  Request
	body *string
	opts RuntimeOptions
	deny *atomic.Bool
}

func newDedupeFixture(t *testing.T) dedupeFixture {
	t.Helper()
	dir, o, r := setup(t)
	deny := &atomic.Bool{}
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if req.Optional && deny.Load() {
			return ErrPolicy
		}
		return nil
	}
	o.EnableEnrichment = true
	q := open(t, dir, o)
	sub := r
	sub.SourceID, sub.Role = "sub", "subagent"
	sub.Path = filepath.Join(filepath.Dir(r.Path), "sub.jsonl")
	write(t, sub.Path, "sub\n")
	body := new(string)
	opts := runtimeDefaults(RuntimeOptions{Enrich: func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{Body: io.NopCloser(strings.NewReader(*body)), Complete: true}, nil
	}})
	return dedupeFixture{q: q, main: r, sub: sub, body: body, opts: opts, deny: deny}
}

// activeSelectedArtifacts counts active artifacts that are selected after policy rehydration.
func (f dedupeFixture) artifacts(t *testing.T) (selected, total int, seq uint64) {
	t.Helper()
	m, es, err := f.q.latestPolicyChecked(t.Context(), f.main.Identity)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range m.Sources {
		if s.Role != "artifact" {
			continue
		}
		total++
		if s.State == "active" && publication.IsSelected(s) && es[i].SelectionReason == "" {
			selected++
		}
	}
	return selected, total, m.Sequence
}

func TestAttachDedupesEqualBytesAcrossSources(t *testing.T) {
	f := newDedupeFixture(t)
	capture(t, f.q, f.main)
	capture(t, f.q, f.sub)
	*f.body = "same-bundle"
	enrichOnce(t, f.q, f.opts)
	sel, total, seq := f.artifacts(t)
	if sel != 1 || total != 1 {
		t.Fatalf("first attach: %d/%d", sel, total)
	}
	enrichOnce(t, f.q, f.opts)
	sel, total, after := f.artifacts(t)
	if sel != 1 || total != 1 || after != seq {
		t.Fatalf("duplicate attached: selected %d total %d seq %d->%d", sel, total, seq, after)
	}
	status, err := f.q.EnrichmentWorkStatus(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range status {
		if s.State != "attached" || s.AttachedSequence != seq {
			t.Fatalf("work %s state %s seq %d", s.ID, s.State, s.AttachedSequence)
		}
	}
	m, _, _ := f.q.latest(t.Context(), f.main.Identity)
	if m.Coverage.Enrichment != "partial" {
		t.Fatal("coverage greened")
	}
}

func TestAttachDifferentBytesStillAttaches(t *testing.T) {
	f := newDedupeFixture(t)
	capture(t, f.q, f.main)
	capture(t, f.q, f.sub)
	*f.body = "one"
	enrichOnce(t, f.q, f.opts)
	*f.body = "two"
	enrichOnce(t, f.q, f.opts)
	if sel, total, _ := f.artifacts(t); sel != 2 || total != 2 {
		t.Fatalf("different bytes: %d/%d", sel, total)
	}
}

func TestAttachDedupeIgnoresWithheldCopy(t *testing.T) {
	f := newDedupeFixture(t)
	capture(t, f.q, f.main)
	capture(t, f.q, f.sub)
	*f.body = "same-bundle"
	enrichOnce(t, f.q, f.opts)
	f.deny.Store(true)
	if _, err := f.q.supersedeOptional(t.Context(), f.main.Identity); err != nil {
		t.Fatal(err)
	}
	f.deny.Store(false)
	if sel, _, _ := f.artifacts(t); sel != 0 {
		t.Fatalf("copy not withheld: %d", sel)
	}
	enrichOnce(t, f.q, f.opts)
	if sel, total, _ := f.artifacts(t); sel != 1 || total != 2 {
		t.Fatalf("withheld copy satisfied dedupe: %d/%d", sel, total)
	}
}

func TestAttachDedupeIgnoresSealedCopy(t *testing.T) {
	f := newDedupeFixture(t)
	capture(t, f.q, f.main)
	*f.body = "same-bundle"
	enrichOnce(t, f.q, f.opts)
	write(t, f.main.Path, "first\nchanged\n")
	capture(t, f.q, f.main)
	*f.body = "replacement"
	enrichOnce(t, f.q, f.opts)
	capture(t, f.q, f.sub)
	*f.body = "same-bundle"
	enrichOnce(t, f.q, f.opts)
	if sel, total, _ := f.artifacts(t); sel != 2 || total != 3 {
		t.Fatalf("sealed copy satisfied dedupe: %d/%d", sel, total)
	}
}
