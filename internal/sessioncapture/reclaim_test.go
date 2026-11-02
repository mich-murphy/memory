package sessioncapture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/publication"
)

// payloadFiles returns the sorted payload file names (encoded hash + .zst).
func payloadFiles(t *testing.T, q *Queue) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(q.dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func payloadFileCount(t *testing.T, q *Queue) int { return len(payloadFiles(t, q)) }

func TestDrainedAppendsLeaveOnlyTheFinalObject(t *testing.T) {
	const k = 12
	dir, o, r := setup(t)
	o.MaxSpoolBytes = 1 << 24
	q := open(t, dir, o)
	capture(t, q, r)
	last := capture(t, q, r)
	for i := 1; i < k; i++ {
		appendBytes(t, r.Path, randomBytes(byte(i), 2048))
		last = capture(t, q, r)
	}
	b := newBackend(t)
	if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	obj := last.Sources[0].Object
	want := []string{obj.EncodedSHA256 + ".zst"}
	if got := payloadFiles(t, q); !cmp.Equal(got, want) {
		t.Fatalf("payload files: %s", cmp.Diff(want, got))
	}
	assertSpoolEqualsRoots(t, q)
	raw, err := os.ReadFile(filepath.Join(q.dir, "payloads", want[0]))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != obj.EncodedSHA256 || int64(len(raw)) != obj.EncodedSize {
		t.Fatalf("retained payload is not the final object (%d bytes)", len(raw))
	}
	if q.used != obj.EncodedSize {
		t.Fatalf("quota used %d, want %d", q.used, obj.EncodedSize)
	}
	rs, err := q.RuntimeStatus(t.Context())
	if err != nil || rs.ReclaimPending != 0 || rs.ReclaimFailed != 0 {
		t.Fatalf("reclaim status %+v %v", rs, err)
	}
}

// Sealed epochs are unique bytes: they stay in the spool while the latest job
// references them, and reopening must not change that or the accounting.
func TestSealedEpochPayloadsAreRetained(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	first := capture(t, q, r)
	write(t, r.Path, "rewritten\n")
	last := capture(t, q, r)
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); err != nil {
		t.Fatal(err)
	}
	want := []string{first.Sources[0].Object.EncodedSHA256 + ".zst", last.Sources[1].Object.EncodedSHA256 + ".zst"}
	slices.Sort(want)
	if got := payloadFiles(t, q); !cmp.Equal(got, want) {
		t.Fatalf("payload files: %s", cmp.Diff(want, got))
	}
	used := q.used
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q2 := open(t, dir, o)
	if q2.used != used || payloadFileCount(t, q2) != 2 {
		t.Fatalf("after reopen used=%d files=%d, want %d and 2", q2.used, payloadFileCount(t, q2), used)
	}
}

// rootObjectFiles returns the payload file names referenced by every undelivered
// job, the latest job and the latest published job of each session.
func rootObjectFiles(t *testing.T, q *Queue) map[string]bool {
	t.Helper()
	rows, err := q.db.QueryContext(t.Context(), `SELECT manifest FROM jobs AS j WHERE state NOT IN ('published','superseded') OR sequence=(SELECT max(sequence) FROM jobs WHERE session=j.session) OR sequence=(SELECT max(sequence) FROM jobs WHERE session=j.session AND state='published')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	out := map[string]bool{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		var m publication.Manifest
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		for i := range m.Sources {
			if sha := m.Sources[i].Object.EncodedSHA256; sha != "" {
				out[sha+".zst"] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertSpoolEqualsRoots is the exact reclamation oracle after a full drain: the
// spool file set equals the objects of the retained roots plus the named extra
// classes passed in retained (enrichment- or repair-held objects, and objects kept
// by selection supersession, which are unique local bytes and are never reclaimed).
// A missing file is a safety failure, an extra one a leak.
func assertSpoolEqualsRoots(t *testing.T, q *Queue, retained ...publication.Object) {
	t.Helper()
	want := rootObjectFiles(t, q)
	for _, o := range retained {
		want[o.EncodedSHA256+".zst"] = true
	}
	var names []string
	for n := range want {
		names = append(names, n)
	}
	slices.Sort(names)
	if got := payloadFiles(t, q); !cmp.Equal(got, names) {
		t.Fatalf("spool file set differs from retained roots: %s", cmp.Diff(names, got))
	}
}

// A failing reclaim after a durable acceptance must not turn that capture into a
// failure or touch job state; the candidate stays recorded and a later pass finishes it.
func TestReclaimFailureAfterAcceptanceIsNotACaptureFailure(t *testing.T) {
	dir, o, r := setup(t)
	failing := false
	o.Fault = func(s string) error {
		if failing && s == "reclaim-before-unlink" {
			return errors.New("injected unlink failure")
		}
		return nil
	}
	q := open(t, dir, o)
	capture(t, q, r)
	appendBytes(t, r.Path, []byte("more\n"))
	failing = true
	last, err := q.Capture(t.Context(), r)
	if err != nil {
		t.Fatalf("capture reported the reclaim failure: %v", err)
	}
	if got, want := jobStates(t, q), (map[string]int{"superseded": 1, "pending": 1}); !cmp.Equal(got, want) {
		t.Fatalf("job states: %s", cmp.Diff(want, got))
	}
	if s := jobStatus(t, q, r.Identity, last.Sequence); s.Attempts != 0 || s.LastError != "" {
		t.Fatalf("job bookkeeping touched: %+v", s)
	}
	rs, err := q.RuntimeStatus(t.Context())
	if err != nil || rs.ReclaimPending != 1 || rs.ReclaimFailed != 1 {
		t.Fatalf("status %+v %v, want 1 pending and 1 failed", rs, err)
	}
	if n := payloadFileCount(t, q); n != 2 {
		t.Fatalf("%d payload files, want 2 while the unlink is failing", n)
	}
	failing = false
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q2 := open(t, dir, o)
	assertSpoolEqualsRoots(t, q2)
	if rs, err := q2.RuntimeStatus(t.Context()); err != nil || rs.ReclaimPending != 0 {
		t.Fatalf("status after reopen %+v %v", rs, err)
	}
}

// Optional artifacts (and objects kept by selection supersession) are named retained
// classes in the oracle: unique local bytes that reclamation never removes.
func TestOptionalArtifactSurvivesDrainOfLaterRawAppends(t *testing.T) {
	q, r, m := optionalFixture(t)
	b := newBackend(t)
	if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		appendBytes(t, r.Path, randomBytes(byte(i), 512))
		capture(t, q, r)
		if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
			t.Fatal(err)
		}
	}
	assertSpoolEqualsRoots(t, q, m.Sources[1].Object)
}

// Replaying a repaired optional repair makes its object needed again, so the
// holder released at "repaired" must be re-established.
func TestReplayedRepairHoldsItsPayloadAgain(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	ctx := t.Context()
	if _, err := q.db.ExecContext(ctx, `INSERT INTO optional_repairs(id,session,source_hash,epoch,commitment,source,evidence,identity,state,stage,contract,software) VALUES('r1','s','h','e','c',?,x'',x'','repaired','st','c','sw')`, []byte(`{"object":{"encoded_sha256":"abc"}}`)); err != nil {
		t.Fatal(err)
	}
	held := func() bool {
		var n int
		if err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM payload_refs WHERE sha='abc' AND session='repair:r1'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	if held() {
		t.Fatal("repaired row must not hold its payload")
	}
	if err := q.ReplayOptionalRepair(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if !held() {
		t.Fatal("replayed repair lost its payload holder")
	}
	if _, err := q.db.ExecContext(ctx, `UPDATE optional_repairs SET state='repaired' WHERE id='r1'`); err != nil {
		t.Fatal(err)
	}
	if held() {
		t.Fatal("repaired row kept its holder")
	}
}

func candidateCount(t *testing.T, q *Queue, where string) int {
	t.Helper()
	var n int
	if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM payload_reclaim WHERE `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A file two sessions share (identical content) must survive one session retiring it.
func TestSharedPayloadSurvivesOneSessionRetiringIt(t *testing.T) {
	dir, o, a := setup(t)
	q := open(t, dir, o)
	b := a
	b.Identity.SessionID = "other"
	b.Path = filepath.Join(filepath.Dir(a.Path), "other.jsonl")
	write(t, b.Path, "first\n")
	ma := capture(t, q, a)
	mb := capture(t, q, b)
	shared := ma.Sources[0].Object.EncodedSHA256
	if shared != mb.Sources[0].Object.EncodedSHA256 {
		t.Fatal("fixture: sessions do not share an encoded payload")
	}
	appendBytes(t, a.Path, []byte("more\n"))
	capture(t, q, a)
	if _, err := os.Stat(filepath.Join(q.dir, "payloads", shared+".zst")); err != nil {
		t.Fatalf("shared payload removed while session B still references it: %v", err)
	}
	if err := q.Deliver(t.Context(), newBackend(t), b.Identity); err != nil {
		t.Fatalf("session B delivery after A retired the shared file: %v", err)
	}
}

// A candidate that a retained root still references is kept and its row dropped.
func TestRootedCandidateIsKept(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, r)
	sha := m.Sources[0].Object.EncodedSHA256
	if _, err := q.db.ExecContext(t.Context(), `INSERT INTO payload_reclaim(sha,session) VALUES(?,?)`, sha, r.Identity.BaseKey()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.reclaimPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(q.dir, "payloads", sha+".zst")); err != nil {
		t.Fatalf("rooted payload removed: %v", err)
	}
	if n := candidateCount(t, q, "1=1"); n != 0 {
		t.Fatalf("%d candidate rows left", n)
	}
}

func TestAddReclaimCandidatesRecordsEveryObject(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	tx, err := q.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	objs := []publication.Object{{EncodedSHA256: "aa"}, {EncodedSHA256: "bb"}, {EncodedSHA256: "cc"}}
	if err := addReclaimCandidates(t.Context(), tx, "s", objs); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := candidateCount(t, q, "1=1"); n != 3 {
		t.Fatalf("%d candidates, want 3", n)
	}
}

// Every failing candidate keeps its own error; one failure never hides the next.
func TestEveryFailedCandidateRecordsItsError(t *testing.T) {
	dir, o, r := setup(t)
	failing := false
	o.Fault = func(s string) error {
		if failing && s == "reclaim-before-unlink" {
			return errors.New("injected unlink failure")
		}
		return nil
	}
	q := open(t, dir, o)
	capture(t, q, r)
	failing = true
	for range 2 {
		appendBytes(t, r.Path, []byte("more\n"))
		capture(t, q, r)
	}
	if n := candidateCount(t, q, "last_error!=''"); n != 2 {
		t.Fatalf("%d candidates carry an error, want 2", n)
	}
}

// The holder index of rows accepted before payload_refs existed is rebuilt at Open.
func TestLegacyJobsAreIndexedAtInit(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, r)
	if _, err := q.db.ExecContext(t.Context(), `DROP TABLE payload_refs`); err != nil {
		t.Fatal(err)
	}
	if err := q.initReclaim(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM payload_refs WHERE sha=? AND session=?`, m.Sources[0].Object.EncodedSHA256, r.Identity.BaseKey()).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backfilled refs %d, err %v", n, err)
	}
}
