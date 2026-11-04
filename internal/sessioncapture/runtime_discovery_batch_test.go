package sessioncapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// discoveryBatchRequests returns n distinct fingerprinted pi-style requests.
func discoveryBatchRequests(t *testing.T, base Request, n int) []Request {
	t.Helper()
	out := make([]Request, n)
	for i := range n {
		x := base
		x.Identity.SessionID = fmt.Sprintf("b%d", i)
		x.SourceID = fmt.Sprintf("bsrc%d", i)
		x.Path = filepath.Join(filepath.Dir(base.Path), fmt.Sprintf("b%d.jsonl", i))
		write(t, x.Path, "data\n")
		x.Fingerprint = fmt.Sprintf("fp%d", i)
		out[i] = x
	}
	return out
}

// Admission of a discovery batch must cost a handful of durable commits, not one
// fsync (synchronous=FULL) per source, so a large cold or changed sweep completes
// inside its operation budget. A settled sweep writes nothing.
func TestDiscoveryAdmissionCommitsInChunksAndSettledSweepWritesNothing(t *testing.T) {
	t.Parallel()
	dir, o, base := setup(t)
	o.MaxInventoryRecords = 1024
	commits := 0
	o.Fault = func(point string) error {
		if point == "runtime-hint-commit" {
			commits++
		}
		return nil
	}
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	reqs := discoveryBatchRequests(t, base, 600)
	batch := func(context.Context) (DiscoveryBatch, error) {
		return DiscoveryBatch{Requests: reqs, Complete: true}, nil
	}
	opts := runtimeDefaults(RuntimeOptions{DiscoverBatch: batch})
	if err := q.discoverOnce(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if commits != 3 { // ceil(600/256)
		t.Fatalf("cold admission commits=%d, want 3", commits)
	}
	st, err := q.RuntimeStatus(t.Context())
	if err != nil || st.Sources != 600 || st.PendingSources != 600 || st.CompletedSweep != 1 || !st.DiscoveryComplete || st.DiscoveryError != "" {
		t.Fatalf("after cold sweep: %+v %v", st, err)
	}
	var versions int
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_sources WHERE version=1`).Scan(&versions); err != nil || versions != 600 {
		t.Fatalf("versions=%d %v", versions, err)
	}
	commits = 0
	if err := q.discoverOnce(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if commits != 0 {
		t.Fatalf("settled sweep committed %d admission transactions", commits)
	}
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_sources WHERE version=1`).Scan(&versions); err != nil || versions != 600 {
		t.Fatalf("unchanged sweep bumped versions: %d %v", versions, err)
	}
	if st, err = q.RuntimeStatus(t.Context()); err != nil || st.CompletedSweep != 2 {
		t.Fatalf("after settled sweep: %+v %v", st, err)
	}
}

func sourceCount(t *testing.T, q *Queue) (n int) {
	t.Helper()
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_sources`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHintBatchIsolatesInvalidRequests(t *testing.T) {
	dir, o, base := setup(t)
	q := open(t, dir, o)
	reqs := discoveryBatchRequests(t, base, 4)
	bad := reqs[1]
	bad.Path = ""
	reqs[1] = bad
	if err := q.HintBatch(t.Context(), reqs); err == nil {
		t.Fatal("invalid request must be reported")
	}
	var got []string
	rows, err := q.db.QueryContext(t.Context(), `SELECT source FROM runtime_sources ORDER BY source`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := "[bsrc0 bsrc2 bsrc3]"; fmt.Sprint(got) != want {
		t.Fatalf("admitted %v, want %s", got, want)
	}
}

func TestHintBatchLiveCapAdmitsExactlyTheFreeSlots(t *testing.T) {
	dir, o, base := setup(t)
	o.MaxInventoryRecords = 5
	q := open(t, dir, o)
	reqs := discoveryBatchRequests(t, base, 12)
	if err := q.HintBatch(t.Context(), reqs[:2]); err != nil {
		t.Fatal(err)
	}
	err := q.HintBatch(t.Context(), reqs[2:])
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("overflow must be ErrQuota: %v", err)
	}
	if n := sourceCount(t, q); n != 5 {
		t.Fatalf("rows=%d, want exactly the cap 5", n)
	}
	// An already-admitted source still coalesces at capacity.
	changed := reqs[0]
	changed.Fingerprint = "changed"
	if err := q.HintBatch(t.Context(), []Request{changed}); err != nil {
		t.Fatalf("existing identity at capacity: %v", err)
	}
	var version int
	if err := q.db.QueryRowContext(t.Context(), `SELECT version FROM runtime_sources WHERE source='bsrc0'`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("changed fingerprint version=%d %v", version, err)
	}
}

func TestHintBatchCrashLosesOnlyUncommittedChunks(t *testing.T) {
	t.Parallel()
	dir, o, base := setup(t)
	o.MaxInventoryRecords = 1024
	commits := 0
	o.Fault = func(point string) error {
		if point != "runtime-hint-commit" {
			return nil
		}
		commits++
		if commits == 2 {
			return errors.New("injected crash")
		}
		return nil
	}
	q := open(t, dir, o)
	reqs := discoveryBatchRequests(t, base, 600)
	if err := q.HintBatch(t.Context(), reqs); err == nil {
		t.Fatal("crash must surface")
	}
	if n := sourceCount(t, q); n != 256+88 {
		t.Fatalf("rows=%d, want chunks 1 and 3 committed (344)", n)
	}
	commits = -100 // no further faults
	if err := q.HintBatch(t.Context(), reqs); err != nil {
		t.Fatal(err)
	}
	if n := sourceCount(t, q); n != 600 {
		t.Fatalf("next sweep re-admits the lost chunk: rows=%d", n)
	}
}

func TestHintBatchRecordsOwnershipAfterAdmission(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	root := t.TempDir()
	sum := sha256.Sum256([]byte(root))
	uuid := "00000001-0000-4000-8000-000000000001"
	legacy := "p/" + uuid
	p := filepath.Join(root, "projects", "p", uuid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, p, "x\n")
	r := Request{Identity: publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "claude", SessionID: "session/" + hex.EncodeToString(sum[:]) + "/" + legacy}, SourceID: "projects/" + legacy + ".jsonl", Role: "main", Path: p, Fingerprint: "f", Associations: []string{"ownership:session", "root:" + root, "adapter-session:" + legacy}}
	if err := q.HintBatch(t.Context(), []Request{r}); err != nil {
		t.Fatal(err)
	}
	session, gotLegacy, err := ReadOwnership(t.Context(), dir, "archive", "claude", root, uuid)
	if err != nil || session != r.Identity.SessionID || gotLegacy != legacy {
		t.Fatalf("ownership %q %q %v", session, gotLegacy, err)
	}
}

// DiscoveryComplete describes the current sweep: a later, still-bounded sweep reports
// false while CompletedSweep keeps the last finished sweep and no error is recorded.
func TestDiscoveryCompleteResetsWhileNextSweepIsBounded(t *testing.T) {
	t.Parallel()
	dir, o, base := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	reqs := discoveryBatchRequests(t, base, 3)
	complete := true
	batch := func(context.Context) (DiscoveryBatch, error) {
		return DiscoveryBatch{Requests: reqs, Complete: complete}, nil
	}
	opts := runtimeDefaults(RuntimeOptions{DiscoverBatch: batch})
	if err := q.discoverOnce(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	complete = false
	if err := q.discoverOnce(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	st, err := q.RuntimeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.CurrentSweep != 2 || st.CompletedSweep != 1 || st.DiscoveryComplete || st.DiscoveryError != "" {
		t.Fatalf("mid-sweep status: current=%d completed=%d complete=%v err=%q, want 2,1,false,\"\"", st.CurrentSweep, st.CompletedSweep, st.DiscoveryComplete, st.DiscoveryError)
	}
	complete = true
	if err := q.discoverOnce(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if st, err = q.RuntimeStatus(t.Context()); err != nil || st.CurrentSweep != 2 || st.CompletedSweep != 2 || !st.DiscoveryComplete {
		t.Fatalf("after finishing: %+v %v", st, err)
	}
}
