package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

type deliveryMutationFixture struct {
	q        *Queue
	b        *publication.Local
	m        publication.Manifest
	es       []evidence
	denied   *atomic.Value
	artifact int
	trusted  map[publication.Object]bool
}

// newDeliveryMutationFixture models a multi-source claude session: a rewritten
// main transcript (sealed epoch), two subagent transcripts and one optional artifact.
// The policy denies a path with a generic (non session-wide) error once armed.
func newDeliveryMutationFixture(t *testing.T, fault func(string) error) *deliveryMutationFixture {
	t.Helper()
	dir, o, r := setup(t)
	f := &deliveryMutationFixture{denied: &atomic.Value{}, trusted: map[publication.Object]bool{}}
	f.denied.Store("")
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if d := f.denied.Load().(string); d != "" && req.Path == d {
			return errors.New("denied by test policy")
		}
		return nil
	}
	o.Fault = fault
	f.q = open(t, dir, o)
	f.b = newBackend(t)
	root := filepath.Dir(r.Path)
	capture(t, f.q, r)
	write(t, r.Path, "first\nrewritten after compaction\n")
	capture(t, f.q, r)
	for n := 1; n <= 2; n++ {
		req := r
		req.SourceID = fmt.Sprintf("subagent-%d", n)
		req.Path = filepath.Join(root, fmt.Sprintf("agent-%d.jsonl", n))
		write(t, req.Path, fmt.Sprintf("subagent %d line\n", n))
		capture(t, f.q, req)
	}
	art := r
	art.SourceID, art.Role, art.Optional = "artifact", "artifact", true
	art.Path = filepath.Join(root, "artifact.bin")
	write(t, art.Path, "optional bytes\n")
	f.m = capture(t, f.q, art)
	var err error
	if _, f.es, err = f.q.latest(t.Context(), r.Identity); err != nil {
		t.Fatal(err)
	}
	f.artifact = -1
	for j := range f.m.Sources {
		s := f.m.Sources[j]
		if publication.IsSelected(s) {
			f.trusted[s.Object] = true
		}
		if s.Optional && publication.IsSelected(s) {
			f.artifact = j
		}
	}
	if f.artifact < 0 || len(f.es) != len(f.m.Sources) {
		t.Fatalf("fixture: artifact %d, evidence %d, sources %d", f.artifact, len(f.es), len(f.m.Sources))
	}
	return f
}

func (f *deliveryMutationFixture) denyArtifact() { f.denied.Store(f.es[f.artifact].Request.Path) }

// A generic policy denial of the last selected (optional) source must stop each
// dependency stage with ErrPolicy, even though the session-scope pass succeeds.
func TestDeliveryStagesRecheckEverySelectedSourceOnSuccessfulScopePass(t *testing.T) {
	stages := map[string]func(*deliveryMutationFixture) error{
		"checkDependencies": func(f *deliveryMutationFixture) error {
			return f.q.checkDependencies(t.Context(), f.m, f.es)
		},
		"uploadDependencies": func(f *deliveryMutationFixture) error {
			return f.q.uploadDependencies(t.Context(), f.b, f.m, f.es, f.trusted)
		},
	}
	for name, stage := range stages {
		t.Run(name, func(t *testing.T) {
			f := newDeliveryMutationFixture(t, nil)
			if err := stage(f); err != nil {
				t.Fatalf("before denial: %v", err)
			}
			f.denyArtifact()
			if err := stage(f); !errors.Is(err, ErrPolicy) || errors.Is(err, ErrSessionExcluded) {
				t.Fatalf("after denial: %v", err)
			}
		})
	}
}

func TestCheckDependenciesRejectsEvidenceCountMismatch(t *testing.T) {
	f := newDeliveryMutationFixture(t, nil)
	err := f.q.checkDependencies(t.Context(), f.m, f.es[:len(f.es)-1])
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("short evidence: %v", err)
	}
}

func TestCheckDependenciesHonoursOptionalUnavailableLatch(t *testing.T) {
	t.Parallel()
	for state, blocked := range map[string]bool{"quarantined": true, "pending": true, "repaired": false, "verified_attachment_pending": false, "delivering": false} {
		t.Run(state, func(t *testing.T) {
			f := newDeliveryMutationFixture(t, nil)
			s := f.m.Sources[f.artifact]
			if _, err := f.q.db.ExecContext(t.Context(), `INSERT INTO optional_repairs(id,session,source_hash,epoch,commitment,source,evidence,identity,state,stage,contract,software) VALUES(?,?,'h','e','c',x'7b7d',x'7b7d',x'7b7d',?,'t','c','v')`, repairID(f.m.Identity, s), f.m.Identity.BaseKey(), state); err != nil {
				t.Fatal(err)
			}
			err := f.q.checkDependencies(t.Context(), f.m, f.es)
			if blocked != errors.Is(err, errOptionalUnavailable) || (!blocked && err != nil) {
				t.Fatalf("state %s: %v", state, err)
			}
		})
	}
}

func TestUploadOneFailsClosedWhenEvidenceSlotMissing(t *testing.T) {
	f := newDeliveryMutationFixture(t, nil)
	s := f.m.Sources[f.artifact]
	for _, j := range []int{len(f.es), len(f.es) + 1} {
		if err := f.q.uploadOne(t.Context(), f.b, f.es, j, &s, false); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("slot %d: %v", j, err)
		}
	}
}

// lateCancelContext reports cancellation through Err only, so database access
// still works while the caller observes that its context has ended.
type lateCancelContext struct {
	context.Context
	canceled atomic.Bool
}

func (c *lateCancelContext) Err() error {
	if c.canceled.Load() {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestDeliverReportsCancellationWhenJobSupersededAtCancel(t *testing.T) {
	ctx := &lateCancelContext{Context: t.Context()}
	var f *deliveryMutationFixture
	f = newDeliveryMutationFixture(t, func(stage string) error {
		if stage != "current-published" {
			return nil
		}
		if _, err := f.q.db.ExecContext(t.Context(), `UPDATE jobs SET state='superseded' WHERE session=?`, f.m.Identity.BaseKey()); err != nil {
			return err
		}
		ctx.canceled.Store(true)
		return nil
	})
	if err := f.q.Deliver(ctx, f.b, f.m.Identity); !errors.Is(err, context.Canceled) {
		t.Fatalf("Deliver: %v", err)
	}
}
