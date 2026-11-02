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
