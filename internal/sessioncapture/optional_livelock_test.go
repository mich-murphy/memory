package sessioncapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/publication"
)

// livelockBackend fails chosen uploads and lets a test act while an upload is
// in flight, as the repair lane does in production.
type livelockBackend struct {
	publication.Backend
	fail  map[string]bool
	onPut func(key string)
}

func (b *livelockBackend) PutFile(ctx context.Context, key, path string) error {
	if b.fail[key] {
		return errors.New("private remote failure")
	}
	if b.onPut != nil {
		b.onPut(key)
	}
	return b.Backend.PutFile(ctx, key, path)
}

type livelockFixture struct {
	q       *Queue
	r       Request
	backend *livelockBackend
	plain   publication.Backend
}

// newLivelockFixture captures a main source and three optional artifacts, then
// delivers while every optional upload fails so all three are withheld.
func newLivelockFixture(t *testing.T) *livelockFixture {
	t.Helper()
	dir, o, r := setup(t)
	q := open(t, dir, o)
	capture(t, q, r)
	for _, id := range []string{"a", "b", "c"} {
		p := filepath.Join(filepath.Dir(r.Path), id+".bin")
		write(t, p, "artifact "+id)
		if _, err := q.Capture(t.Context(), Request{Identity: r.Identity, SourceID: id, Role: "artifact", Path: p, Optional: true}); err != nil {
			t.Fatal(err)
		}
	}
	m, _, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	plain := newBackend(t)
	f := &livelockFixture{q: q, r: r, plain: plain, backend: &livelockBackend{Backend: plain, fail: map[string]bool{}}}
	for n := range m.Sources {
		if s := &m.Sources[n]; s.Optional {
			f.backend.fail[s.Object.Key] = true
		}
	}
	for range 8 {
		if err := q.Deliver(t.Context(), f.backend, r.Identity); err != nil {
			t.Fatal(err)
		}
	}
	clear(f.backend.fail)
	if got := f.selectedOptional(t); len(got) != 0 {
		t.Fatalf("setup: %d optional sources still selected", len(got))
	}
	return f
}

func (f *livelockFixture) selectedOptional(t *testing.T) []string {
	t.Helper()
	m, _, err := f.q.latest(t.Context(), f.r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for n := range m.Sources {
		if s := &m.Sources[n]; s.Optional && publication.IsSelected(*s) {
			keys = append(keys, s.Object.Key)
		}
	}
	return keys
}

func (f *livelockFixture) repair(t *testing.T) {
	t.Helper()
	did, err := f.q.optionalRepairNext(t.Context(), f.plain, runtimeDefaults(RuntimeOptions{}))
	if err != nil || !did {
		t.Fatalf("repair did=%v err=%v", did, err)
	}
}

func (f *livelockFixture) states(t *testing.T) []string {
	t.Helper()
	rows, err := f.q.db.QueryContext(t.Context(), `SELECT state FROM optional_repairs ORDER BY state`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *livelockFixture) remoteSelected(t *testing.T) int {
	t.Helper()
	m, err := publication.Read(t.Context(), f.plain, f.r.Identity)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return -1
		}
		t.Fatal(err)
	}
	n := 0
	for j := range m.Sources {
		if s := &m.Sources[j]; s.Optional && publication.IsSelected(*s) {
			n++
		}
	}
	return n
}

// A repair that attaches another epoch supersedes the delivery in flight. The
// superseded delivery must not re-latch an epoch a repair already verified.
func TestSupersededDeliveryKeepsRepairedOptionalEpochs(t *testing.T) {
	f := newLivelockFixture(t)
	f.repair(t)
	selected := f.selectedOptional(t)
	if len(selected) != 1 {
		t.Fatalf("selected after first repair: %d", len(selected))
	}
	if diff := cmp.Diff([]string{"pending", "pending", "repaired"}, f.states(t)); diff != "" {
		t.Fatalf("states before delivery (-want +got):\n%s", diff)
	}
	fired := false
	f.backend.onPut = func(key string) {
		if key == selected[0] && !fired {
			fired = true
			f.repair(t)
		}
	}
	if err := f.q.Deliver(t.Context(), f.backend, f.r.Identity); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("repair never ran during delivery")
	}
	if diff := cmp.Diff([]string{"pending", "repaired", "repaired"}, f.states(t)); diff != "" {
		t.Fatalf("states after superseded delivery (-want +got):\n%s", diff)
	}
	// Safety: the unrepaired epoch stays withheld and is never published.
	if got := len(f.selectedOptional(t)); got != 2 {
		t.Fatalf("selected after superseded delivery: %d, want 2", got)
	}
	if err := f.q.Deliver(t.Context(), f.backend, f.r.Identity); err != nil {
		t.Fatal(err)
	}
	if got := f.remoteSelected(t); got != 2 {
		t.Fatalf("published optional sources: %d, want 2", got)
	}
}

// With a repair landing during every optional upload, the session still
// publishes every repaired epoch within a bounded number of rounds and goes idle.
func TestRepairsDuringDeliveryConvergeAndGoIdle(t *testing.T) {
	f := newLivelockFixture(t)
	f.backend.onPut = func(string) {
		if _, err := f.q.optionalRepairNext(t.Context(), f.plain, runtimeDefaults(RuntimeOptions{})); err != nil {
			t.Error(err)
		}
	}
	f.repair(t)
	for round := 1; round <= 6; round++ {
		if err := f.q.Deliver(t.Context(), f.backend, f.r.Identity); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.remoteSelected(t); got != 3 {
		t.Fatalf("published optional sources after 6 rounds: %d, want 3", got)
	}
	if diff := cmp.Diff([]string{"repaired", "repaired", "repaired"}, f.states(t)); diff != "" {
		t.Fatalf("final states (-want +got):\n%s", diff)
	}
	var open int
	if err := f.q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM jobs WHERE state NOT IN ('published','superseded')`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("unfinished jobs %d: %v", open, err)
	}
	var maxSeq uint64
	if err := f.q.db.QueryRowContext(t.Context(), `SELECT max(sequence) FROM jobs`).Scan(&maxSeq); err != nil {
		t.Fatal(err)
	}
	if err := f.q.Deliver(t.Context(), f.backend, f.r.Identity); err != nil {
		t.Fatal(err)
	}
	var after uint64
	if err := f.q.db.QueryRowContext(t.Context(), `SELECT max(sequence) FROM jobs`).Scan(&after); err != nil || after != maxSeq {
		t.Fatalf("idle delivery created jobs: %d -> %d (%v)", maxSeq, after, err)
	}
}

// A delivery that completes (not superseded) settles a row a repair had verified
// but not yet attached as repaired, rather than restoring its prior state.
func TestCompletedDeliverySettlesVerifiedAttachmentPending(t *testing.T) {
	f := newLivelockFixture(t)
	f.repair(t)
	if _, err := f.q.db.ExecContext(t.Context(), `UPDATE optional_repairs SET state='verified_attachment_pending' WHERE state='repaired'`); err != nil {
		t.Fatal(err)
	}
	if err := f.q.Deliver(t.Context(), f.backend, f.r.Identity); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"pending", "pending", "repaired"}, f.states(t)); diff != "" {
		t.Fatalf("states after completed delivery (-want +got):\n%s", diff)
	}
	if got := f.remoteSelected(t); got != 1 {
		t.Fatalf("published optional sources: %d, want 1", got)
	}
}
