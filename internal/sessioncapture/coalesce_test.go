package sessioncapture

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/publication"
)

// meterBackend counts remote operations so coalescing is asserted exactly.
type meterBackend struct {
	publication.Backend
	objPuts, metaPuts, swaps int
}

func (b *meterBackend) Sync(ctx context.Context, key string) error {
	if d, ok := b.Backend.(publication.DurabilityFence); ok {
		return d.Sync(ctx, key)
	}
	return nil
}

func (b *meterBackend) PutFile(ctx context.Context, key, path string) error {
	if strings.Contains(key, "/objects/") {
		b.objPuts++
	} else {
		b.metaPuts++
	}
	return b.Backend.PutFile(ctx, key, path)
}

func (b *meterBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	b.swaps++
	return b.Backend.CompareAndSwap(ctx, key, token, body)
}

// randomBytes returns n deterministic bytes that do not compress.
func randomBytes(seed byte, n int) []byte {
	b := make([]byte, n)
	_, _ = rand.NewChaCha8([32]byte{seed}).Read(b)
	return b
}

func appendBytes(t *testing.T, p string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(b); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func jobStates(t *testing.T, q *Queue) map[string]int {
	t.Helper()
	ss, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for i := range ss {
		out[ss[i].State]++
	}
	return out
}

// normalized zeroes the fields that legitimately differ between a coalesced
// history and one immediate final capture: sequence, timestamps, the identifiers
// derived from them, and the revision count of the source epoch.
func normalized(m publication.Manifest) publication.Manifest {
	m.Sequence, m.PublicationID = 0, ""
	m.ObservedAtNS, m.CapturedAtNS, m.PublishedAtNS = 0, 0, 0
	m.Sources = append([]publication.Source(nil), m.Sources...)
	for i := range m.Sources {
		m.Sources[i].EpochID, m.Sources[i].Revision = "", 0
		m.Sources[i].ObservedAtNS, m.Sources[i].CapturedAtNS = 0, 0
	}
	return m
}

func publishedManifest(t *testing.T, b publication.Backend, id publication.Identity) publication.Manifest {
	t.Helper()
	m, err := publication.Read(t.Context(), b, id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDeferredAppendsDeliverAsOneObjectAndOneSwap(t *testing.T) {
	const k = 50
	dir, o, r := setup(t)
	o.MaxSpoolBytes = 1 << 26
	q := open(t, dir, o)
	capture(t, q, r)
	for i := 1; i < k; i++ {
		appendBytes(t, r.Path, randomBytes(byte(i), 1024))
		capture(t, q, r)
	}
	if got, want := jobStates(t, q), (map[string]int{"pending": 1, "superseded": k - 1}); !cmp.Equal(got, want) {
		t.Fatalf("job states: %s", cmp.Diff(want, got))
	}
	m := &meterBackend{Backend: newBackend(t)}
	if err := q.Deliver(t.Context(), m, r.Identity); err != nil {
		t.Fatal(err)
	}
	if m.objPuts != 1 || m.swaps != 1 || m.metaPuts != 1 {
		t.Fatalf("remote operations: objects=%d manifests=%d cas=%d, want 1 each", m.objPuts, m.metaPuts, m.swaps)
	}
	// Reference: the same final file captured once, immediately.
	refDir, refOpts, refReq := setup(t)
	refReq.Path = r.Path
	refQ := open(t, refDir, refOpts)
	capture(t, refQ, refReq)
	refBackend := newBackend(t)
	if err := refQ.Deliver(t.Context(), refBackend, r.Identity); err != nil {
		t.Fatal(err)
	}
	got := normalized(publishedManifest(t, m, r.Identity))
	want := normalized(publishedManifest(t, refBackend, r.Identity))
	if d := cmp.Diff(want, got); d != "" {
		t.Fatalf("coalesced publication differs from an immediate final capture:\n%s", d)
	}
}

func TestRewriteEpochsSurviveCoalescing(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	first := capture(t, q, r)
	write(t, r.Path, "rewritten\n")
	capture(t, q, r)
	appendBytes(t, r.Path, []byte("more\n"))
	last := capture(t, q, r)
	if got, want := jobStates(t, q), (map[string]int{"pending": 1, "superseded": 2}); !cmp.Equal(got, want) {
		t.Fatalf("job states: %s", cmp.Diff(want, got))
	}
	b := newBackend(t)
	if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	m := publishedManifest(t, b, r.Identity)
	if m.Sequence != last.Sequence || len(m.Sources) != 2 {
		t.Fatalf("published sequence %d with %d sources, want %d with 2", m.Sequence, len(m.Sources), last.Sequence)
	}
	sealed, active := m.Sources[0], m.Sources[1]
	if sealed.State != "sealed" || sealed.Object != first.Sources[0].Object {
		t.Fatalf("sealed epoch %q object %+v, want the original bytes %+v", sealed.State, sealed.Object, first.Sources[0].Object)
	}
	if active.State != "active" || active.Object != last.Sources[1].Object {
		t.Fatalf("active epoch %q object %+v, want %+v", active.State, active.Object, last.Sources[1].Object)
	}
}

// A capture that lands while an older job is mid-delivery must never let the
// stale job replace newer content, whichever side of the supersede guard it hits.
// At current-before the stale CAS has passed its guard and does land remotely;
// the local job must stay superseded and job 2 must then replace it.
func TestAppendDuringDeliveryNeverLetsStaleWorkWin(t *testing.T) {
	for _, stage := range []string{"upload-before", "manifest-before", "current-before"} {
		t.Run(stage, func(t *testing.T) {
			dir, o, r := setup(t)
			var q *Queue
			var second publication.Manifest
			fired := false
			o.Fault = func(s string) error {
				if s != stage || fired {
					return nil
				}
				fired = true
				appendBytes(t, r.Path, []byte("second\n"))
				m, err := q.Capture(t.Context(), r)
				second = m
				return err
			}
			q = open(t, dir, o)
			capture(t, q, r)
			b := newBackend(t)
			if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
				t.Fatal(err)
			}
			if !fired {
				t.Fatalf("fault %s never reached", stage)
			}
			if got := jobStatus(t, q, r.Identity, 1).State; got != "superseded" {
				t.Fatalf("job 1 state %s, want superseded", got)
			}
			if got := jobStatus(t, q, r.Identity, 2).State; got != "pending" {
				t.Fatalf("job 2 state %s, want pending", got)
			}
			if got, want := remoteCurrentSequence(t, b, r.Identity), map[string]uint64{"manifest-before": 0, "current-before": 1}[stage]; got != want {
				t.Fatalf("remote current sequence %d after stale attempt, want %d", got, want)
			}
			if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
				t.Fatal(err)
			}
			if got := jobStatus(t, q, r.Identity, 1).State; got != "superseded" {
				t.Fatalf("job 1 state %s after job 2 acknowledged, want superseded", got)
			}
			if got := jobStatus(t, q, r.Identity, 2).State; got != "published" {
				t.Fatalf("job 2 state %s, want published", got)
			}
			body, _, err := b.Current(t.Context(), r.Identity.CurrentKey())
			if err != nil {
				t.Fatal(err)
			}
			var cur publication.Current
			if err := json.Unmarshal(body, &cur); err != nil {
				t.Fatal(err)
			}
			if cur.Sequence != 2 || cur.PublicationID != second.PublicationID {
				t.Fatalf("remote current %d/%s, want job 2 %d/%s", cur.Sequence, cur.PublicationID, second.Sequence, second.PublicationID)
			}
			want, err := os.ReadFile(filepath.Clean(r.Path))
			if err != nil {
				t.Fatal(err)
			}
			if m := publishedManifest(t, b, r.Identity); m.Sources[0].Object.Size != int64(len(want)) {
				t.Fatalf("published size %d, want %d", m.Sources[0].Object.Size, len(want))
			}
			// The stale job's payload was reclaimed when it was superseded; only job 2's remains.
			if n := payloadFileCount(t, q); n != 1 {
				t.Fatalf("%d payload files after stale attempt, want 1", n)
			}
		})
	}
}

// remoteCurrentSequence returns 0 when nothing has been published yet.
func remoteCurrentSequence(t *testing.T, b publication.Backend, id publication.Identity) uint64 {
	t.Helper()
	body, _, err := b.Current(t.Context(), id.CurrentKey())
	if errors.Is(err, publication.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	var cur publication.Current
	if err := json.Unmarshal(body, &cur); err != nil {
		t.Fatal(err)
	}
	return cur.Sequence
}

func TestSubsumes(t *testing.T) {
	obj := func(tag string, size int64) publication.Object {
		return publication.Object{Key: "k/" + tag, Encoding: "zstd", SHA256: tag, EncodedSHA256: "e" + tag, Size: size, EncodedSize: size}
	}
	src := func(id, epoch, state string, rev uint64, o publication.Object) publication.Source {
		return publication.Source{SourceID: id, EpochID: epoch, State: state, Revision: rev, Object: o}
	}
	view := func(seq uint64, ss ...publication.Source) publication.Manifest {
		return publication.Manifest{Sequence: seq, Sources: ss}
	}
	withheld := src("a", "e1", "active", 1, obj("a", 1))
	withheld.Withheld = &publication.WithheldSource{}
	optional := src("a", "e1", "active", 1, obj("a", 1))
	optional.Optional = true
	cases := []struct {
		name      string
		old, next publication.Manifest
		want      bool
	}{
		{"append", view(1, src("a", "e1", "active", 1, obj("a", 1))), view(2, src("a", "e1", "active", 2, obj("b", 2))), true},
		{"same revision same object", view(1, src("a", "e1", "active", 1, obj("a", 1))), view(2, src("a", "e1", "active", 1, obj("a", 1))), true},
		{"same revision other object", view(1, src("a", "e1", "active", 1, obj("a", 1))), view(2, src("a", "e1", "active", 1, obj("b", 1))), false},
		{"newer revision smaller object", view(1, src("a", "e1", "active", 1, obj("a", 5))), view(2, src("a", "e1", "active", 2, obj("b", 4))), false},
		{"older revision", view(1, src("a", "e1", "active", 3, obj("a", 1))), view(2, src("a", "e1", "active", 2, obj("b", 9))), false},
		{"sealed since", view(1, src("a", "e1", "active", 1, obj("a", 1))), view(2, src("a", "e1", "sealed", 1, obj("a", 1)), src("a", "e2", "active", 1, obj("c", 1))), true},
		{"sealed identical", view(1, src("a", "e1", "sealed", 1, obj("a", 1))), view(2, src("a", "e1", "sealed", 1, obj("a", 1))), true},
		{"sealed changed", view(1, src("a", "e1", "sealed", 1, obj("a", 1))), view(2, src("a", "e1", "sealed", 1, obj("b", 1))), false},
		{"sealed became active", view(1, src("a", "e1", "sealed", 1, obj("a", 1))), view(2, src("a", "e1", "active", 2, obj("b", 2))), false},
		{"epoch missing", view(1, src("a", "e1", "active", 1, obj("a", 1))), view(2, src("a", "e2", "active", 1, obj("b", 1))), false},
		{"other source id", view(1, src("a", "e1", "active", 1, obj("a", 1))), view(2, src("z", "e1", "active", 1, obj("a", 1))), false},
		{"unknown state", view(1, src("a", "e1", "weird", 1, obj("a", 1))), view(2, src("a", "e1", "active", 2, obj("b", 2))), false},
		{"withheld old", view(1, withheld), view(2, withheld), false},
		{"optional old", view(1, optional), view(2, optional), false},
		{"optional new", view(1, src("a", "e1", "active", 1, obj("a", 1))), view(2, optional), false},
		{"not newer", view(2, src("a", "e1", "active", 1, obj("a", 1))), view(2, src("a", "e1", "active", 2, obj("b", 2))), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := subsumes(c.next, c.old); got != c.want {
				t.Fatalf("subsumes = %v, want %v", got, c.want)
			}
		})
	}
}

// Selection supersession (optional detach, terminal repair quota) must retire every
// undelivered job, subsumable or not. A skipped job is redelivered forever because
// each supersession accepts a new job (livelock).
func TestSelectionSupersessionSupersedesEveryUndeliveredJob(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	capture(t, q, r)
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET enrichment_gap='quota' WHERE sequence=1`); err != nil {
		t.Fatal(err)
	}
	m, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	if _, err := q.acceptView(t.Context(), m, es, "", true); err != nil {
		t.Fatal(err)
	}
	if got := jobStatus(t, q, r.Identity, 1).State; got != "superseded" {
		t.Fatalf("job 1 state %s, want superseded", got)
	}
	if got := jobStatus(t, q, r.Identity, 2).State; got != "pending" {
		t.Fatalf("job 2 state %s, want pending", got)
	}
}

// One acceptance retires every subsumed undelivered job, and only pending or retry
// jobs are coalesced: a quarantined job is left alone.
func TestRetireJobsHandlesEachJobAndOnlyPlainStates(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  map[string]int
	}{
		{"retry", map[string]int{"superseded": 2, "pending": 1}},
		{"quarantined", map[string]int{"quarantined": 1, "superseded": 1, "pending": 1}},
	} {
		t.Run(tc.state, func(t *testing.T) {
			dir, o, r := setup(t)
			q := open(t, dir, o)
			capture(t, q, r)
			appendBytes(t, r.Path, []byte("two\n"))
			capture(t, q, r)
			if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state=? WHERE sequence=1`, tc.state); err != nil {
				t.Fatal(err)
			}
			if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state='pending' WHERE sequence=2`); err != nil {
				t.Fatal(err)
			}
			appendBytes(t, r.Path, []byte("three\n"))
			capture(t, q, r)
			if got := jobStates(t, q); !cmp.Equal(got, tc.want) {
				t.Fatalf("job states: %s", cmp.Diff(tc.want, got))
			}
		})
	}
}
