package cli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func ownerFixture(t *testing.T, owners int) (capturedaemon.Config, string, runtimeComposition, []string) {
	t.Helper()
	c, config := runtimeFixture(t)
	c.EntriesPerRoot = 1024
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	mains := writeOwnerTree(t, c, owners)
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	return c, config, r, mains
}

// artifactLines counts admitted external artifact requests of owner i.
func artifactLines(proj []string, i int) int {
	n := 0
	for _, line := range proj {
		if strings.HasPrefix(line, "req|") && strings.Contains(line, ownerTestUUID(i)) && !strings.Contains(line, "/projects/") {
			n++
		}
	}
	return n
}

func hasIssue(proj []string, code string) bool {
	for _, line := range proj {
		if strings.HasPrefix(line, "issue|") && strings.HasSuffix(line, "|"+code) {
			return true
		}
	}
	return false
}

func bothPaths(t *testing.T, r runtimeComposition, config string) (snap []string) {
	t.Helper()
	snap = discoverProjection(t, r.callOptions(r.sourceOptions(config)))
	live := discoverProjection(t, r.sourceOptions(config))
	if d := cmp.Diff(live, snap); d != "" {
		t.Fatalf("memoized discovery differs from live policy (-live +snapshot):\n%s", d)
	}
	return snap
}

type ownerCase struct {
	name       string
	setup      func(t *testing.T, c capturedaemon.Config, r runtimeComposition, config string, mains []string)
	want       string // issue code on every owner-0 artifact; "" means all admitted
	otherAdmit bool   // owner 1 is recorded and unaffected
}

func excludeOwner(t *testing.T, c capturedaemon.Config, i int, main string) {
	t.Helper()
	root := filepath.Join(c.Home, ".claude")
	_, legacy, err := sessioncapture.ReadOwnership(t.Context(), c.Spool, c.ArchiveID, "claude", root, ownerTestUUID(i))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exclusion.RecordFile(c.PolicyDir, adapters.SessionFile{Agent: "claude", Root: root, SessionKey: legacy, Path: main}); err != nil {
		t.Fatal(err)
	}
}

// artifactCodes returns the issue codes of owner i's external artifacts.
func artifactCodes(proj []string, i int) []string {
	var codes []string
	for _, line := range proj {
		if strings.HasPrefix(line, "issue|") && strings.Contains(line, ownerTestUUID(i)) && !strings.Contains(line, "/projects/") {
			codes = append(codes, line[strings.LastIndex(line, "|")+1:])
		}
	}
	slices.Sort(codes)
	return codes
}

// Each ownership outcome gives the same Discover requests and issues through the
// per-call memo as through live policy, and the expected non-empty outcome.
func TestDiscoveryOwnershipEquivalenceMemoVersusLive(t *testing.T) {
	cases := []ownerCase{
		{"owned", func(t *testing.T, _ capturedaemon.Config, r runtimeComposition, config string, m []string) {
			recordOwners(t, r, config, m[0], m[1])
		}, "", true},
		{"pending", func(t *testing.T, _ capturedaemon.Config, r runtimeComposition, config string, m []string) {
			recordOwners(t, r, config, m[1])
		}, "ownership_pending", true},
		{"ambiguous", func(t *testing.T, c capturedaemon.Config, r runtimeComposition, config string, m []string) {
			alt := ownerMainPath(c, "q", 0)
			mustWrite(t, alt)
			recordOwners(t, r, config, m[0], alt, m[1])
		}, "ownership_ambiguous", true},
		{"excluded owner", func(t *testing.T, c capturedaemon.Config, r runtimeComposition, config string, m []string) {
			recordOwners(t, r, config, m[0], m[1])
			excludeOwner(t, c, 0, m[0])
		}, "policy_denied", false}, // owner 1 is also withheld by the saved exclusion on both paths; equivalence is asserted, not owner 1
		{"policy-denied main", func(t *testing.T, c capturedaemon.Config, r runtimeComposition, config string, m []string) {
			recordOwners(t, r, config, m[0], m[1])
			target := filepath.Join(c.Spool, "denied.jsonl")
			mustWrite(t, target)
			if err := os.Remove(m[0]); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, m[0]); err != nil {
				t.Fatal(err)
			}
		}, "policy_denied", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, config, r, mains := ownerFixture(t, 2)
			tc.setup(t, c, r, config, mains)
			snap := bothPaths(t, r, config)
			codes := artifactCodes(snap, 0)
			if tc.want == "" {
				if got := artifactLines(snap, 0); got != externalPerOwner || len(codes) != 0 {
					t.Errorf("owner 0 admitted = %d issues = %q, want %d admitted", got, codes, externalPerOwner)
				}
			} else if got := artifactLines(snap, 0); got != 0 || len(codes) != externalPerOwner || slices.Compact(slices.Clone(codes))[0] != tc.want || len(slices.Compact(slices.Clone(codes))) != 1 {
				t.Errorf("owner 0 admitted = %d codes = %q, want 0 admitted and %d x %s", got, codes, externalPerOwner, tc.want)
			}
			if got := artifactLines(snap, 1); tc.otherAdmit && got != externalPerOwner {
				t.Errorf("unrelated owner 1 admitted = %d, want %d", got, externalPerOwner)
			}
		})
	}
}

// An archive binding mismatch yields pending ownership identically on both paths.
func TestDiscoveryOwnershipBindingMismatchEquivalent(t *testing.T) {
	c, config, r, mains := ownerFixture(t, 2)
	recordOwners(t, r, config, mains...)
	db, err := sql.Open("sqlite", filepath.Join(c.Spool, "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE binding SET archive='other'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snap := bothPaths(t, r, config)
	if artifactLines(snap, 0)+artifactLines(snap, 1) != 0 || !hasIssue(snap, "ownership_pending") {
		t.Fatalf("binding mismatch must leave artifacts pending: %q", snap)
	}
}

// Ownership changes and exclusions saved between calls take effect on the next call.
func TestDiscoveryOwnershipFreshAcrossCalls(t *testing.T) {
	c, config, r, mains := ownerFixture(t, 2)
	root := filepath.Join(c.Home, ".claude")
	call := func() []string { return discoverProjection(t, r.callOptions(r.sourceOptions(config))) }
	if p := call(); artifactLines(p, 0) != 0 || !hasIssue(p, "ownership_pending") {
		t.Fatalf("call 1: unowned artifacts must be pending: %q", p)
	}
	recordOwners(t, r, config, mains[0], mains[1])
	if p := call(); artifactLines(p, 0) != externalPerOwner || artifactLines(p, 1) != externalPerOwner {
		t.Fatalf("call 2: pending->owned not seen: %q", p)
	}
	_, legacy, err := sessioncapture.ReadOwnership(t.Context(), c.Spool, c.ArchiveID, "claude", root, ownerTestUUID(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exclusion.RecordFile(c.PolicyDir, adapters.SessionFile{Agent: "claude", Root: root, SessionKey: legacy, Path: mains[1]}); err != nil {
		t.Fatal(err)
	}
	alt := ownerMainPath(c, "q", 0)
	mustWrite(t, alt)
	recordOwners(t, r, config, alt)
	p := call()
	if artifactLines(p, 0) != 0 || artifactLines(p, 1) != 0 || !hasIssue(p, "ownership_ambiguous") {
		t.Fatalf("call 3: owned->ambiguous and saved exclusion not seen: %q", p)
	}
}

// One discovery call reads each distinct owner once, however many artifacts share it.
func TestDiscoveryCallReadsEachOwnerOnce(t *testing.T) {
	_, config, r, mains := ownerFixture(t, 6)
	recordOwners(t, r, config, mains[:4]...)
	var mu sync.Mutex
	reads := map[ownerKey]int{}
	live := r.owners()
	r.ownerLookup = func(ctx_ context.Context, agent, root, uuid string) (string, string, error) {
		mu.Lock()
		reads[ownerKey{agent, root, uuid}]++
		mu.Unlock()
		return live(ctx_, agent, root, uuid)
	}
	proj := discoverProjection(t, r.callOptions(r.sourceOptions(config)))
	if artifactLines(proj, 0) != externalPerOwner {
		t.Fatalf("fixture not discovered: %q", proj)
	}
	if len(reads) != 6 {
		t.Errorf("distinct owners read = %d, want 6", len(reads))
	}
	for k, n := range reads {
		if n != 1 {
			t.Errorf("owner %s read %d times in one call, want 1", k.uuid, n)
		}
	}
}
