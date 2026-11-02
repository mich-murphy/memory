package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// settledFixture hints, captures and publishes n distinct sessions.
func settledFixture(t *testing.T, q *Queue, base Request, n int) []Request {
	t.Helper()
	b := newBackend(t)
	var out []Request
	for i := range n {
		x := base
		x.Identity.SessionID = fmt.Sprintf("s%d", i)
		x.SourceID = fmt.Sprintf("src%d", i)
		x.Path = filepath.Join(filepath.Dir(base.Path), fmt.Sprintf("f%d.jsonl", i))
		write(t, x.Path, "data\n")
		x.Fingerprint = fmt.Sprintf("fp%d", i)
		x.ObservedAtNS = int64(i + 1)
		capturedOnce(t, q, x)
		if err := q.DeliverDue(t.Context(), b, x.Identity); err != nil {
			t.Fatal(err)
		}
		out = append(out, x)
	}
	return out
}

func rowCounts(t *testing.T, q *Queue) (total, live, settled int) {
	t.Helper()
	err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*),COALESCE(SUM(state NOT IN ('captured','absent')),0),COALESCE(SUM(state IN ('captured','absent')),0) FROM runtime_sources`).Scan(&total, &live, &settled)
	if err != nil {
		t.Fatal(err)
	}
	return total, live, settled
}

func TestInventoryAdmissionIgnoresSettledHistory(t *testing.T) {
	dir, o, r := setup(t)
	o.MaxInventoryRecords = 8
	q := open(t, dir, o)
	settledFixture(t, q, r, 8)
	nine := r
	nine.Identity.SessionID, nine.SourceID, nine.Path = "new", "new", filepath.Join(filepath.Dir(r.Path), "new.jsonl")
	write(t, nine.Path, "new\n")
	if err := q.Hint(t.Context(), nine); err != nil {
		t.Fatalf("new session refused behind settled history: %v", err)
	}
	if total, live, settled := rowCounts(t, q); total != 9 || live != 1 || settled != 8 {
		t.Fatalf("rows total=%d live=%d settled=%d", total, live, settled)
	}
	st, err := q.RuntimeStatus(t.Context())
	if err != nil || st.SourceCap != 8 || st.LiveSources != 1 || st.SettledSources != 8 {
		t.Fatalf("occupancy %+v %v", st, err)
	}
}

func TestInventoryLiveWorkFullStillRefusesVisibly(t *testing.T) {
	dir, o, r := setup(t)
	o.MaxInventoryRecords = 3
	q := open(t, dir, o)
	settledFixture(t, q, r, 3)
	for i := range 3 {
		x := r
		x.Identity.SessionID, x.SourceID = fmt.Sprintf("live%d", i), fmt.Sprintf("live%d", i)
		if err := q.Hint(t.Context(), x); err != nil {
			t.Fatalf("live %d: %v", i, err)
		}
	}
	x := r
	x.Identity.SessionID, x.SourceID = "overflow", "overflow"
	if err := q.Hint(t.Context(), x); !errors.Is(err, ErrQuota) {
		t.Fatalf("live-full cap must refuse with ErrQuota: %v", err)
	}
	st, err := q.RuntimeStatus(t.Context())
	if err != nil || st.SourceCap != 3 || st.LiveSources != 3 || st.SettledSources != 3 || st.Sources != 6 {
		t.Fatalf("occupancy %+v %v", st, err)
	}
}

// sweepOnce runs one complete unchanged discovery sweep over reqs and reports the
// rows re-hinted as live work, the captures that followed and the evictions it made.
func sweepOnce(t *testing.T, q *Queue, reqs []Request) (rehinted, captures int, evictions int64) {
	t.Helper()
	before, err := q.RuntimeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch := func(context.Context) (DiscoveryBatch, error) {
		return DiscoveryBatch{Requests: reqs, Complete: true}, nil
	}
	if err := q.discoverOnce(t.Context(), runtimeDefaults(RuntimeOptions{DiscoverBatch: batch})); err != nil {
		t.Fatal(err)
	}
	_, live, _ := rowCounts(t, q)
	rehinted = live
	for {
		ok, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{}))
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		captures++
	}
	after, err := q.RuntimeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rehinted, captures, after.InventoryEvictions - before.InventoryEvictions
}

func jobCount(t *testing.T, q *Queue) (n int) {
	t.Helper()
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInventoryDefaultSettledBoundIsTheCeiling(t *testing.T) {
	dir, o, _ := setup(t)
	if got := open(t, dir, o).settledBound(); got != MaxInventoryRecords {
		t.Fatalf("default settled bound %d", got)
	}
}

func TestInventoryUnchangedSweepsWithinBoundDoNothing(t *testing.T) {
	dir, o, r := setup(t)
	o.MaxInventoryRecords = 8
	q := open(t, dir, o)
	reqs := settledFixture(t, q, r, 6)
	q.settledLimit = 6
	for i := range 3 {
		if rehinted, captures, evictions := sweepOnce(t, q, reqs); rehinted != 0 || captures != 0 || evictions != 0 {
			t.Fatalf("sweep %d: rehinted=%d captures=%d evictions=%d", i, rehinted, captures, evictions)
		}
	}
	if total, live, settled := rowCounts(t, q); total != 6 || live != 0 || settled != 6 || jobCount(t, q) != 6 {
		t.Fatalf("rows total=%d live=%d settled=%d", total, live, settled)
	}
}

// Beyond the bound, eviction rotates: every other sweep the two evicted sources
// re-hint and recapture to the unchanged manifest (no new job); evictions are counted.
func TestInventoryOverBoundThrashIsBoundedAndVisible(t *testing.T) {
	dir, o, r := setup(t)
	o.MaxInventoryRecords = 8
	q := open(t, dir, o)
	reqs := settledFixture(t, q, r, 6)
	q.settledLimit = 4
	want := [][3]int64{{0, 0, 2}, {2, 2, 0}, {0, 0, 2}, {2, 2, 0}}
	for i, w := range want {
		rehinted, captures, evictions := sweepOnce(t, q, reqs)
		if int64(rehinted) != w[0] || int64(captures) != w[1] || evictions != w[2] {
			t.Fatalf("sweep %d: rehinted=%d captures=%d evictions=%d, want %v", i, rehinted, captures, evictions, w)
		}
	}
	if jobCount(t, q) != 6 {
		t.Fatalf("recapture of unchanged bytes created jobs: %d", jobCount(t, q))
	}
	st, err := q.RuntimeStatus(t.Context())
	if err != nil || st.InventoryEvictions != 4 {
		t.Fatalf("evictions %+v %v", st, err)
	}
}

func sourceNames(t *testing.T, q *Queue) []string {
	t.Helper()
	rows, err := q.db.QueryContext(t.Context(), `SELECT source FROM runtime_sources ORDER BY observed_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
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

func evictFixture(t *testing.T) (*Queue, []Request) {
	t.Helper()
	dir, o, r := setup(t)
	o.MaxInventoryRecords = 8
	q := open(t, dir, o)
	reqs := settledFixture(t, q, r, 5)
	for i := range reqs {
		if _, err := q.db.ExecContext(t.Context(), `UPDATE runtime_sources SET observed_at=? WHERE source=?`, 10+i, reqs[i].SourceID); err != nil {
			t.Fatal(err)
		}
	}
	q.settledLimit = 3
	return q, reqs
}

func TestInventoryEvictsAbsentFirstThenLeastRecentlyObserved(t *testing.T) {
	q, reqs := evictFixture(t)
	if err := q.HintAbsent(t.Context(), reqs[4]); err != nil { // newest observation, but absent
		t.Fatal(err)
	}
	n, err := q.evictSettledSources(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("evicted %d %v", n, err)
	}
	if got, want := fmt.Sprint(sourceNames(t, q)), "[src1 src2 src3]"; got != want {
		t.Fatalf("kept %s, want %s", got, want)
	}
}

func TestInventoryEvictionSkipsClaimedRows(t *testing.T) {
	q, _ := evictFixture(t)
	if _, err := q.db.ExecContext(t.Context(), `UPDATE runtime_sources SET claim=1 WHERE source='src0'`); err != nil {
		t.Fatal(err)
	}
	n, err := q.evictSettledSources(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("evicted %d %v", n, err)
	}
	if got, want := fmt.Sprint(sourceNames(t, q)), "[src0 src3 src4]"; got != want {
		t.Fatalf("kept %s, want %s", got, want)
	}
}

func TestInventoryEvictionSkipsActiveQuarantineRows(t *testing.T) {
	q, _ := evictFixture(t)
	for i, active := range []int{1, 0} { // src0 active entry protects; src1 resolved entry does not
		if _, err := q.db.ExecContext(t.Context(), `INSERT INTO runtime_quarantine(kind,source_id,session,reason,attempts,created_at,contract,active) SELECT 'capture',id,session,'x',1,1,'c',? FROM runtime_sources WHERE source=?`, active, fmt.Sprintf("src%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := q.evictSettledSources(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("evicted %d %v", n, err)
	}
	if got, want := fmt.Sprint(sourceNames(t, q)), "[src0 src3 src4]"; got != want {
		t.Fatalf("kept %s, want %s", got, want)
	}
}

func TestInventoryEvictionAtCeilingCost(t *testing.T) {
	dir, o, _ := setup(t)
	o.MaxInventoryRecords = MaxInventoryRecords
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO runtime_sources(session,source,request,observed_at,state,captured_version) SELECT 'old',CAST(x AS TEXT),'{}',x,'captured',1 FROM n`, MaxInventoryRecords+1)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := q.evictSettledSources(t.Context())
	t.Logf("evicting 1 of %d settled rows: %v", MaxInventoryRecords+1, time.Since(start))
	if err != nil || n != 1 {
		t.Fatalf("evicted %d %v", n, err)
	}
	var oldest int
	if err := q.db.QueryRowContext(t.Context(), `SELECT MIN(observed_at) FROM runtime_sources`).Scan(&oldest); err != nil || oldest != 2 {
		t.Fatalf("oldest remaining %d %v", oldest, err)
	}
	start = time.Now()
	n, err = q.evictSettledSources(t.Context())
	t.Logf("no-op check at %d settled rows: %v", MaxInventoryRecords, time.Since(start))
	if err != nil || n != 0 {
		t.Fatalf("second eviction %d %v", n, err)
	}
}

func TestInventoryAdmissionAtCeilingWithSettledRows(t *testing.T) {
	dir, o, r := setup(t)
	o.MaxInventoryRecords = MaxInventoryRecords
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO runtime_sources(session,source,request,observed_at,state,captured_version) SELECT 'old',CAST(x AS TEXT),'{}',x,'captured',1 FROM n`, MaxInventoryRecords)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatalf("admission with %d settled rows: %v", MaxInventoryRecords, err)
	}
	t.Logf("admission with %d settled rows: %v", MaxInventoryRecords, time.Since(start))
	if total, live, settled := rowCounts(t, q); total != MaxInventoryRecords+1 || live != 1 || settled != MaxInventoryRecords {
		t.Fatalf("rows total=%d live=%d settled=%d", total, live, settled)
	}
}
