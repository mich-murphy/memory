package sessioncapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func rebaseAppend(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func jobState(t *testing.T, q *Queue, id publication.Identity, seq uint64) string {
	t.Helper()
	ss, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := range ss {
		if ss[i].Identity == id && ss[i].Sequence == seq {
			return ss[i].State
		}
	}
	t.Fatalf("no job %d", seq)
	return ""
}

// Adapted from the E2 acceptance test: a rebuilt local spool whose sequence numbers
// are behind the remote current must converge by rebasing above the remote, keeping
// every remote epoch.
func TestRebuiltSpoolRebasesAboveRemoteCurrent(t *testing.T) {
	for _, variant := range []string{"append_same_source", "foreign_writer_different_bytes"} {
		t.Run(variant, func(t *testing.T) {
			dir, o, r := setup(t)
			b := newBackend(t)
			qa := open(t, dir, o)
			ra := r
			if variant != "append_same_source" {
				ra.Path = filepath.Join(filepath.Dir(r.Path), "foreign.jsonl")
				write(t, ra.Path, "foreign\n")
			}
			for i := 1; i <= 3; i++ {
				if i > 1 {
					rebaseAppend(t, ra.Path, fmt.Sprintf("line%d\n", i))
				}
				capture(t, qa, ra)
				if err := qa.Deliver(t.Context(), b, ra.Identity); err != nil {
					t.Fatalf("queue A deliver %d: %v", i, err)
				}
			}
			remote, err := publication.Read(t.Context(), b, r.Identity)
			if err != nil || remote.Sequence != 3 || len(remote.Sources) != 1 {
				t.Fatalf("remote seq=%d sources=%d err=%v", remote.Sequence, len(remote.Sources), err)
			}
			old := remote.Sources[0]
			qb := open(t, filepath.Join(t.TempDir(), "queue"), o)
			capture(t, qb, r)
			rebaseAppend(t, r.Path, "after-rebuild\n")
			capture(t, qb, r)
			if err := qb.Deliver(t.Context(), b, r.Identity); err != nil {
				t.Fatalf("rebuilt spool deliver: %v", err)
			}
			// Job 1 is proven subsumed by job 2 (GAP-03), so only job 2 is delivered;
			// it rebases one sequence above the remote current (3).
			if s := jobState(t, qb, r.Identity, 1); s != "superseded" {
				t.Fatalf("job 1 state=%s, want superseded", s)
			}
			if s := jobState(t, qb, r.Identity, 2); s != "published" {
				t.Fatalf("job 2 state=%s, want published", s)
			}
			got, err := publication.Read(t.Context(), b, r.Identity)
			if err != nil || got.Sequence != 4 || len(got.Sources) != 2 {
				t.Fatalf("final seq=%d sources=%d err=%v", got.Sequence, len(got.Sources), err)
			}
			retained, active := got.Sources[0], got.Sources[1]
			if retained.EpochID != old.EpochID || retained.State != "sealed" || retained.Revision != old.Revision || retained.Object != old.Object {
				t.Fatalf("remote epoch not retained: %+v vs %+v", retained, old)
			}
			if active.State != "active" || active.Revision != 2 || active.EpochID == old.EpochID {
				t.Fatalf("local epoch: %+v", active)
			}
		})
	}
}

func rebaseSource(id, epoch string, rev uint64, sha string) publication.Source {
	return publication.Source{
		SourceID: id, EpochID: epoch, Role: "main", Path: "/p/" + id, State: "active", Revision: rev,
		Object: publication.Object{Key: "k/" + sha, Encoding: "zstd", SHA256: sha, EncodedSHA256: sha},
	}
}

func TestRebaseManifestRetainsRemoteEpochsAndSealsSuperseded(t *testing.T) {
	remote := publication.Manifest{Sequence: 7, Sources: []publication.Source{rebaseSource("a", "e1", 3, "r1"), rebaseSource("b", "e9", 1, "r9")}}
	local := publication.Manifest{Sequence: 2, Coverage: publication.Coverage{Discovery: "partial", Enrichment: "partial"}, Sources: []publication.Source{rebaseSource("a", "e2", 2, "l2")}}
	got, origin, covered, err := rebaseManifest(remote, local)
	if err != nil || covered {
		t.Fatalf("%v covered=%v", err, covered)
	}
	if got.Sequence != 8 || got.PublicationID == "" || fmt.Sprint(origin) != "[-1 -1 0]" {
		t.Fatalf("seq=%d id=%q origin=%v", got.Sequence, got.PublicationID, origin)
	}
	var parts []string
	for i := range got.Sources {
		s := &got.Sources[i]
		parts = append(parts, fmt.Sprintf("%s:%s:%d", s.EpochID, s.State, s.Revision))
	}
	if states := strings.Join(parts, " "); states != "e1:sealed:3 e9:active:1 e2:active:2" {
		t.Fatalf("sources: %s", states)
	}
}

func TestRebaseManifestCoveredWhenRemoteExtendsLocal(t *testing.T) {
	remote := publication.Manifest{Sequence: 4, Sources: []publication.Source{rebaseSource("a", "e1", 3, "r3")}}
	local := publication.Manifest{Sequence: 1, Sources: []publication.Source{rebaseSource("a", "e1", 1, "r1")}}
	if _, _, covered, err := rebaseManifest(remote, local); err != nil || !covered {
		t.Fatalf("covered=%v %v", covered, err)
	}
	local.Sources[0].Revision = 3
	local.Sources[0].Object = remote.Sources[0].Object
	if _, _, covered, err := rebaseManifest(remote, local); err != nil || !covered {
		t.Fatalf("identical revision must be covered: %v %v", covered, err)
	}
}

func TestRebaseManifestForeignDivergenceStaysConflict(t *testing.T) {
	remote := publication.Manifest{Sequence: 4, Sources: []publication.Source{rebaseSource("a", "e1", 3, "remote-bytes")}}
	local := publication.Manifest{Sequence: 9, Sources: []publication.Source{rebaseSource("a", "e1", 3, "other-bytes")}}
	_, _, _, err := rebaseManifest(remote, local)
	if !errors.Is(err, publication.ErrConflict) || classify(err, nil) != FailureConflict {
		t.Fatalf("err=%v class=%s", err, classify(err, nil))
	}
}

// sealedRemote is n sealed remote sources. Sealed epochs cannot be superseded, so
// sealSuperseded skips them instead of hashing every active pair (O(n^2) sha256
// at the 1024 bound); the size limit itself is checked on the unchanged path.
func sealedRemote(n int) []publication.Source {
	out := make([]publication.Source, n)
	for i := range out {
		out[i] = rebaseSource(fmt.Sprintf("s%d", i), "e", 1, fmt.Sprintf("h%d", i))
		out[i].State = "sealed"
	}
	return out
}

func TestRebaseManifestOverSourceLimitIsTypedQuota(t *testing.T) {
	remote := publication.Manifest{Sequence: 4, Sources: sealedRemote(publication.MaxSources)}
	local := publication.Manifest{Sequence: 1, Sources: []publication.Source{rebaseSource("new", "e", 1, "n")}}
	_, _, _, err := rebaseManifest(remote, local)
	if !errors.Is(err, ErrQuota) || classify(err, nil) != FailureQuota {
		t.Fatalf("err=%v class=%s", err, classify(err, nil))
	}
}

type countBackend struct {
	publication.Backend
	current, opens, puts, swaps int
	beforeSwap                  func()
}

func (b *countBackend) Sync(ctx context.Context, key string) error {
	if d, ok := b.Backend.(publication.DurabilityFence); ok {
		return d.Sync(ctx, key)
	}
	return nil
}

func (b *countBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	b.opens++
	return b.Backend.Open(ctx, key)
}

func (b *countBackend) PutFile(ctx context.Context, key, path string) error {
	b.puts++
	return b.Backend.PutFile(ctx, key, path)
}

func (b *countBackend) Current(ctx context.Context, key string) ([]byte, string, error) {
	b.current++
	return b.Backend.Current(ctx, key)
}

func (b *countBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	b.swaps++
	if f := b.beforeSwap; f != nil {
		b.beforeSwap = nil
		f()
	}
	return b.Backend.CompareAndSwap(ctx, key, token, body)
}

// remoteAhead delivers n captures of one source from a first spool.
func remoteAhead(t *testing.T, n int) (*publication.Local, *Queue, Options, Request) {
	t.Helper()
	dir, o, r := setup(t)
	b := newBackend(t)
	qa := open(t, dir, o)
	for i := 1; i <= n; i++ {
		if i > 1 {
			rebaseAppend(t, r.Path, fmt.Sprintf("line%d\n", i))
		}
		capture(t, qa, r)
		if err := qa.Deliver(t.Context(), b, r.Identity); err != nil {
			t.Fatal(err)
		}
	}
	return b, qa, o, r
}

func remoteOf(t *testing.T, b publication.Backend, r Request) publication.Manifest {
	t.Helper()
	m, err := publication.Read(t.Context(), b, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRebaseRecheckPolicyOfRemoteOnlySourceFailsClosed(t *testing.T) {
	dir, o, r := setup(t)
	b := newBackend(t)
	qa := open(t, dir, o)
	other := r
	other.SourceID = "other"
	other.Path = filepath.Join(filepath.Dir(r.Path), "other.jsonl")
	write(t, other.Path, "other\n")
	capture(t, qa, other)
	if err := qa.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	o2 := o
	o2.CheckPolicy = func(_ context.Context, rq Request) error {
		if rq.SourceID == "other" {
			return ErrSessionExcluded
		}
		return nil
	}
	cb := &countBackend{Backend: b}
	qb := open(t, filepath.Join(t.TempDir(), "queue"), o2)
	capture(t, qb, r)
	err := qb.Deliver(t.Context(), cb, r.Identity)
	if !errors.Is(err, ErrPolicy) || !errors.Is(err, ErrSessionExcluded) {
		t.Fatalf("err=%v", err)
	}
	if cb.swaps != 0 || cb.puts != 0 || remoteOf(t, b, r).Sequence != 1 {
		t.Fatalf("nothing may be published: swaps=%d puts=%d", cb.swaps, cb.puts)
	}
}

func TestRebaseSteadyStateEachCaptureIsOneStep(t *testing.T) {
	b, _, o, r := remoteAhead(t, 3)
	first := remoteOf(t, b, r).Sources[0]
	qb := open(t, filepath.Join(t.TempDir(), "queue"), o)
	var costs []string
	for i := 1; i <= 3; i++ {
		if i > 1 {
			rebaseAppend(t, r.Path, fmt.Sprintf("b%d\n", i))
		}
		capture(t, qb, r)
		cb := &countBackend{Backend: b}
		if err := qb.Deliver(t.Context(), cb, r.Identity); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		m := remoteOf(t, b, r)
		if m.Sequence != uint64(3+i) || len(m.Sources) != 2 || m.Sources[0].EpochID != first.EpochID || m.Sources[0].Object != first.Object || m.Sources[1].Revision != uint64(i) {
			t.Fatalf("step %d: seq=%d sources=%+v", i, m.Sequence, m.Sources)
		}
		costs = append(costs, fmt.Sprintf("current=%d opens=%d puts=%d swaps=%d", cb.current, cb.opens, cb.puts, cb.swaps))
	}
	// Bounded, measured cost: every delivery repeats Current+Read+merge because local
	// sequencing stays below the remote. This must not grow silently.
	want := []string{"current=5 opens=6 puts=1 swaps=1", "current=5 opens=8 puts=2 swaps=1", "current=5 opens=8 puts=2 swaps=1"}
	if fmt.Sprint(costs) != fmt.Sprint(want) {
		t.Fatalf("per-delivery backend calls %v, want %v", costs, want)
	}
}

func TestRebaseCoveredPublishesWithoutWrites(t *testing.T) {
	dir, o, r := setup(t)
	b := newBackend(t)
	qa := open(t, dir, o)
	capture(t, qa, r)
	rebaseAppend(t, r.Path, "more\n")
	capture(t, qa, r)
	if err := qa.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	write(t, r.Path, "first\n") // the rebuilt spool sees the first revision's bytes again
	qb := open(t, filepath.Join(t.TempDir(), "queue"), o)
	capture(t, qb, r)
	cb := &countBackend{Backend: b}
	if err := qb.Deliver(t.Context(), cb, r.Identity); err != nil {
		t.Fatal(err)
	}
	if s := jobState(t, qb, r.Identity, 1); s != "published" || cb.swaps != 0 || cb.puts != 0 || remoteOf(t, b, r).Sequence != 2 {
		t.Fatalf("state=%s swaps=%d puts=%d", s, cb.swaps, cb.puts)
	}
}

func TestRebaseConvergesWhenCurrentMovesBeforeSwap(t *testing.T) {
	b, qa, o, r := remoteAhead(t, 2)
	cb := &countBackend{Backend: b}
	cb.beforeSwap = func() {
		rebaseAppend(t, r.Path, "concurrent\n")
		capture(t, qa, r)
		if err := qa.Deliver(t.Context(), b, r.Identity); err != nil {
			t.Error(err)
		}
	}
	qb := open(t, filepath.Join(t.TempDir(), "queue"), o)
	capture(t, qb, r)
	err := qb.Deliver(t.Context(), cb, r.Identity)
	if err == nil {
		t.Fatal("stale token must not publish")
	}
	if remoteOf(t, b, r).Sequence != 3 {
		t.Fatalf("concurrent writer's publication must stand")
	}
	if err := qb.Deliver(t.Context(), cb, r.Identity); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if m := remoteOf(t, b, r); m.Sequence != 4 || jobState(t, qb, r.Identity, 1) != "published" {
		t.Fatalf("seq=%d", m.Sequence)
	}
}

func TestRebaseLostAckIsCoveredOnRetry(t *testing.T) {
	b, _, o, r := remoteAhead(t, 2)
	failed := false
	o.Fault = func(point string) error {
		if point == "current-published" && !failed {
			failed = true
			return errors.New("lost ack")
		}
		return nil
	}
	cb := &countBackend{Backend: b}
	qb := open(t, filepath.Join(t.TempDir(), "queue"), o)
	capture(t, qb, r)
	if err := qb.Deliver(t.Context(), cb, r.Identity); err == nil {
		t.Fatal("lost ack must surface")
	}
	if cb.swaps != 1 || remoteOf(t, b, r).Sequence != 3 || jobState(t, qb, r.Identity, 1) == "published" {
		t.Fatalf("swaps=%d", cb.swaps)
	}
	if err := qb.Deliver(t.Context(), cb, r.Identity); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if cb.swaps != 1 || jobState(t, qb, r.Identity, 1) != "published" || remoteOf(t, b, r).Sequence != 3 {
		t.Fatalf("retry must be covered: swaps=%d", cb.swaps)
	}
}

// A lost acknowledgement inside the guarded write is resolved by Publish's read-back:
// one step, one CAS, published.
func TestRebaseLostAckAtCurrentAfterResolvesInOneStep(t *testing.T) {
	b, _, o, r := remoteAhead(t, 2)
	o.Fault = func(point string) error {
		if point == "current-after" {
			return errors.New("lost ack")
		}
		return nil
	}
	cb := &countBackend{Backend: b}
	qb := open(t, filepath.Join(t.TempDir(), "queue"), o)
	capture(t, qb, r)
	if err := qb.Deliver(t.Context(), cb, r.Identity); err != nil {
		t.Fatal(err)
	}
	if cb.swaps != 1 || jobState(t, qb, r.Identity, 1) != "published" || remoteOf(t, b, r).Sequence != 3 {
		t.Fatalf("swaps=%d", cb.swaps)
	}
}

func TestRebaseManifestExactlyMaxSourcesFits(t *testing.T) {
	remote := publication.Manifest{Sequence: 4, Sources: sealedRemote(publication.MaxSources - 1)}
	local := publication.Manifest{Sequence: 1, Sources: []publication.Source{rebaseSource("new", "e", 1, "n")}}
	got, _, covered, err := rebaseManifest(remote, local)
	if err != nil || covered || len(got.Sources) != publication.MaxSources {
		t.Fatalf("sources=%d covered=%v err=%v", len(got.Sources), covered, err)
	}
}

func TestRebaseCoverageMergesDiscoveryAndEnrichmentIndependently(t *testing.T) {
	for _, tc := range []struct{ remote, local, want [2]string }{
		{[2]string{"complete", "complete"}, [2]string{"complete", "complete"}, [2]string{"complete", "complete"}},
		{[2]string{"partial", "complete"}, [2]string{"complete", "complete"}, [2]string{"partial", "complete"}},
		{[2]string{"complete", "partial"}, [2]string{"complete", "complete"}, [2]string{"complete", "partial"}},
	} {
		remote := publication.Manifest{Sequence: 4, Coverage: publication.Coverage{Discovery: tc.remote[0], Enrichment: tc.remote[1]}, Sources: []publication.Source{rebaseSource("a", "e1", 1, "r")}}
		local := publication.Manifest{Sequence: 1, Coverage: publication.Coverage{Discovery: tc.local[0], Enrichment: tc.local[1]}, Sources: []publication.Source{rebaseSource("b", "e2", 1, "l")}}
		got, _, _, err := rebaseManifest(remote, local)
		if err != nil || [2]string{got.Coverage.Discovery, got.Coverage.Enrichment} != tc.want {
			t.Fatalf("%v: coverage %+v err=%v", tc, got.Coverage, err)
		}
	}
}

func TestRebasedEvidenceKeepsLocalEvidenceAtIndexZero(t *testing.T) {
	merged := publication.Manifest{Identity: publication.Identity{SessionID: "s"}, Sources: []publication.Source{rebaseSource("a", "e1", 1, "l"), rebaseSource("b", "e2", 1, "r")}}
	es := []evidence{{EpochID: "e1", Request: Request{Path: "/local/a"}}}
	got := rebasedEvidence(merged, []int{0, -1}, es)
	if got[0].Request.Path != "/local/a" || got[1].Request.Path != "/p/b" || got[1].Request.SourceID != "b" || got[1].EpochID != "e2" {
		t.Fatalf("%+v", got)
	}
}

// The metadata bound is inclusive: a merged manifest of exactly MaxMetadata bytes fits,
// one byte more is a typed quota error.
func TestRebaseManifestMetadataBoundaryIsInclusive(t *testing.T) {
	remote := publication.Manifest{Sequence: 4, Sources: []publication.Source{rebaseSource("a", "e1", 1, "r")}}
	build := func(pad int) (publication.Manifest, error) {
		s := rebaseSource("b", "e2", 1, "l")
		s.Associations = []string{strings.Repeat("x", pad)}
		got, _, _, err := rebaseManifest(remote, publication.Manifest{Sequence: 1, Sources: []publication.Source{s}})
		return got, err
	}
	got, err := build(10)
	if err != nil {
		t.Fatal(err)
	}
	got.PublicationID = ""
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	pad := 10 + publication.MaxMetadata - len(b)
	if _, err := build(pad); err != nil {
		t.Fatalf("exactly MaxMetadata must fit: %v", err)
	}
	if _, err := build(pad + 1); !errors.Is(err, ErrQuota) {
		t.Fatalf("MaxMetadata+1 must be ErrQuota: %v", err)
	}
}
