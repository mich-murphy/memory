package sessioncapture

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/publication"
)

type quotaClock struct{ ns atomic.Int64 }

func newQuotaClock() *quotaClock {
	c := &quotaClock{}
	c.ns.Store(time.Unix(100, 0).UnixNano())
	return c
}

func (c *quotaClock) now() time.Time { return time.Unix(0, c.ns.Load()) }

func (c *quotaClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

func inboxJSONNames(t *testing.T, inbox string) []string {
	t.Helper()
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names
}

func countRows(t *testing.T, q *Queue, table string) int {
	t.Helper()
	var n int
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNativeInboxQuotaBlockedFilesBackOffUntilCapacityChanges(t *testing.T) {
	dir, o, _ := setup(t)
	clock := newQuotaClock()
	var policyCalls atomic.Int64
	o.Now = clock.now
	o.CheckPolicy = func(context.Context, Request) error { policyCalls.Add(1); return nil }
	q := open(t, dir, o)
	if err := q.setEnrichmentLimit(2); err != nil {
		t.Fatal(err)
	}
	identity := publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "claude", SessionID: "session"}
	var names []string
	for _, id := range []string{"launch-a", "launch-b", "launch-c"} {
		// Resolve symlinks (macOS /var -> /private/var) so the request path has exactly one identity.
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		absent := filepath.Join(base, "absent")
		if resolved, err := filepath.EvalSymlinks(base); err != nil || resolved != base {
			t.Fatalf("temp dir %q is not symlink-free (%q, %v); callbacksPerAttempt assumes one identity", base, resolved, err)
		}
		record := NativeEvidence{Contract: EnrichmentContract, ID: id, Identity: identity, Request: Request{Identity: identity, SourceID: "source", Role: "main", Path: absent}, ObservedAtNS: 1, Context: NativeContext{Event: "start", ContextOrigin: "native"}}
		if err := WriteNativeEvidence(t.Context(), dir, record); err != nil {
			t.Fatal(err)
		}
		names = append(names, digest([]byte(id))+".json")
	}
	slices.Sort(names)
	inbox := filepath.Join(dir, "native-evidence")
	drain := func() {
		t.Helper()
		if err := q.drainNativeEvidence(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Each AcceptNativeEvidence attempt runs the policy callback twice.
	const callbacksPerAttempt = 2
	expectCalls := func(attempts int64) {
		t.Helper()
		if got, want := policyCalls.Load(), attempts*callbacksPerAttempt; got != want {
			t.Fatalf("policy callbacks %d, want %d", got, want)
		}
	}
	expectFiles := func(want []string) {
		t.Helper()
		if diff := cmp.Diff(want, inboxJSONNames(t, inbox)); diff != "" {
			t.Fatalf("inbox files (-want +got):\n%s", diff)
		}
	}

	drain()
	expectCalls(3)
	expectFiles(names[1:])
	if got := countRows(t, q, "enrichment_work"); got != 2 {
		t.Fatalf("work rows %d", got)
	}
	for range 5 {
		drain()
	}
	expectCalls(3)
	clock.advance(999 * time.Millisecond)
	drain()
	expectCalls(3)
	clock.advance(time.Millisecond)
	drain()
	expectCalls(5)
	expectFiles(names[1:])
	clock.advance(time.Second)
	drain()
	expectCalls(5)
	clock.advance(time.Second)
	drain()
	expectCalls(7)
	expectFiles(names[1:])

	if err := q.setEnrichmentLimit(6); err != nil {
		t.Fatal(err)
	}
	drain()
	expectCalls(9)
	expectFiles(nil)
	if got := countRows(t, q, "enrichment_work"); got != 6 {
		t.Fatalf("work rows %d after capacity change", got)
	}
	if got := countRows(t, q, "native_evidence"); got != 3 {
		t.Fatalf("native rows %d", got)
	}
}

func TestQuotaGappedJobAdmissionBacksOffUntilCapacityChanges(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	clock := newQuotaClock()
	var attempts atomic.Int64
	o.Now = clock.now
	o.Fault = func(s string) error {
		if s == "enrichment-admission-attempt" {
			attempts.Add(1)
		}
		return nil
	}
	q := open(t, dir, o)
	if err := q.setEnrichmentLimit(1); err != nil {
		t.Fatal(err)
	}
	capture(t, q, r)
	work := snapshotOptions("body")
	enrichOnce(t, q, work)
	write(t, r.Path, "first\nappend\n")
	m := capture(t, q, r)
	gap := func() string {
		t.Helper()
		var g string
		if err := q.db.QueryRowContext(t.Context(), `SELECT enrichment_gap FROM jobs WHERE sequence=?`, m.Sequence).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	if got := gap(); got != "quota" {
		t.Fatalf("gap %q", got)
	}
	base := attempts.Load()
	tick := func(wantDid bool, wantAttempts int64) {
		t.Helper()
		did, err := q.enrichmentNext(t.Context(), work)
		if err != nil || did != wantDid {
			t.Fatalf("tick did=%v: %v", did, err)
		}
		if got := attempts.Load() - base; got != wantAttempts {
			t.Fatalf("admission attempts %d, want %d", got, wantAttempts)
		}
	}
	for range 5 {
		tick(false, 1)
	}
	clock.advance(time.Second)
	tick(false, 2)
	clock.advance(time.Second)
	tick(false, 2)
	clock.advance(time.Second)
	tick(false, 3)
	if got := gap(); got != "quota" {
		t.Fatalf("gap cleared without capacity: %q", got)
	}
	if err := q.setEnrichmentLimit(2); err != nil {
		t.Fatal(err)
	}
	tick(true, 4)
	if got := gap(); got != "" {
		t.Fatalf("gap %q after capacity change", got)
	}
}

func TestEnrichmentLimitBounds(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if err := q.setEnrichmentLimit(MaxEnrichmentWork + 1); err == nil {
		t.Fatal("limit above MaxEnrichmentWork accepted")
	}
	if err := q.setEnrichmentLimit(-1); err == nil {
		t.Fatal("negative limit accepted")
	}
	if err := q.setEnrichmentLimit(0); err != nil || q.effectiveEnrichmentLimit() != MaxEnrichmentWork {
		t.Fatalf("default limit %d: %v", q.effectiveEnrichmentLimit(), err)
	}
}
