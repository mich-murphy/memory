package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type headTestBackend struct {
	Backend
	prefixes     []string
	done         bool
	listErr      error
	currentErr   error
	active, peak atomic.Int32
	calls        atomic.Int32
}

func (b *headTestBackend) ListSessionNamespaces(_ context.Context, _ Identity, _ string, _ int) ([]string, bool, error) {
	return b.prefixes, b.done, b.listErr
}

func (b *headTestBackend) Current(ctx context.Context, key string) ([]byte, string, error) {
	b.calls.Add(1)
	n := b.active.Add(1)
	defer b.active.Add(-1)
	for old := b.peak.Load(); n > old; old = b.peak.Load() {
		if b.peak.CompareAndSwap(old, n) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	if b.currentErr != nil {
		return nil, "", b.currentErr
	}
	return b.Backend.Current(ctx, key)
}

func TestCurrentHeadsPaginationAndValidation(t *testing.T) {
	local, m, _ := fixture(t)
	publish(t, local, m, "")
	b := &headTestBackend{Backend: local, done: true}
	cursor := ""
	for _, agent := range []string{"claude", "codex", "pi"} {
		b.prefixes = nil
		if agent == "pi" {
			b.prefixes = []string{m.Identity.BaseKey() + "/"}
		}
		page, err := ReadCurrentHeads(t.Context(), b, m.Identity.ArchiveID, m.Identity.MachineID, cursor, 256)
		if err != nil {
			t.Fatal(err)
		}
		if page.Complete != (agent == "pi") || len(page.Heads) != len(b.prefixes) {
			t.Fatalf("bad page %+v", page)
		}
		if len(page.Heads) > 0 && (page.Heads[0].Identity != m.Identity || page.Heads[0].Sequence != m.Sequence || page.Heads[0].PublicationID != m.PublicationID) {
			t.Fatal("wrong head")
		}
		cursor = page.NextCursor
	}
	for _, s := range []string{"bad", cursor + "=", strings.Repeat("a", 4097)} {
		if _, err := ReadCurrentHeads(t.Context(), b, "archive", "machine", s, 1); err == nil {
			t.Fatal("bad cursor accepted")
		}
	}
	if _, err := ReadCurrentHeads(t.Context(), b, "other", "machine", cursor, 1); err == nil {
		t.Fatal("cross archive cursor")
	}
	if _, err := ReadCurrentHeads(t.Context(), b, "archive", "other", cursor, 1); err == nil {
		t.Fatal("cross machine cursor")
	}
	for _, limit := range []int{0, 257} {
		if _, err := ReadCurrentHeads(t.Context(), b, "archive", "machine", "", limit); err == nil {
			t.Fatal("bad limit")
		}
	}
	if _, err := ReadCurrentHeads(t.Context(), local, "archive", "machine", "", 1); !errors.Is(err, ErrListingUnsupported) {
		t.Fatal(err)
	}
}

func TestCurrentHeadsBoundedConcurrencyOrphansAndErrors(t *testing.T) {
	local, m, _ := fixture(t)
	b := &headTestBackend{Backend: local, done: true}
	scope := m.Identity
	scope.Agent = "claude"
	for n := range 32 {
		id := scope
		id.SessionID = fmt.Sprintf("session-%02d", n)
		b.prefixes = append(b.prefixes, id.BaseKey()+"/")
		c := Current{Contract: Contract, Identity: id, PublicationID: "pub", Sequence: 1, ManifestSHA256: strings.Repeat("a", 64)}
		c.ManifestKey = id.BaseKey() + "/manifests/" + c.ManifestSHA256 + ".json"
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			if err = local.CompareAndSwap(t.Context(), id.CurrentKey(), "", data); err != nil {
				t.Fatal(err)
			}
		}
	}
	page, err := ReadCurrentHeads(t.Context(), b, "archive", "machine", "", 32)
	if err != nil || len(page.Heads) != 31 || b.peak.Load() > 8 || b.peak.Load() < 2 {
		t.Fatalf("heads=%d peak=%d err=%v", len(page.Heads), b.peak.Load(), err)
	}
	// No manifests exist: reference-only discovery must still succeed.
	b.currentErr = errors.New("unavailable")
	if page, err = ReadCurrentHeads(t.Context(), b, "archive", "machine", "", 32); err != nil || len(page.Failures) != 32 || len(page.Heads) != 0 {
		t.Fatal("failures not retained", err, page)
	}
	b.currentErr = nil
	cases := [][]string{{b.prefixes[0], b.prefixes[0]}, {b.prefixes[1], b.prefixes[0]}, {scope.BaseKey() + "/objects/"}, {strings.Replace(b.prefixes[0], "/sessions/", "/sessions/FF", 1)}}
	for _, prefixes := range cases {
		b.prefixes = prefixes
		if _, err = ReadCurrentHeads(t.Context(), b, "archive", "machine", "", 32); err == nil {
			t.Fatal("invalid listing accepted")
		}
	}
	b.prefixes = nil
	b.done = false
	if _, err = ReadCurrentHeads(t.Context(), b, "archive", "machine", "", 32); err == nil {
		t.Fatal("empty nonadvancing page")
	}
}

func TestCurrentHeadUsesAuthoritativeValidation(t *testing.T) {
	for _, mutate := range []func(*Current){
		func(c *Current) { c.Contract = "unknown" }, func(c *Current) { c.Identity.MachineID = "other" }, func(c *Current) { c.Sequence = 0 }, func(c *Current) { c.PublicationID = "" }, func(c *Current) { c.PublicationID = "\x00" }, func(c *Current) { c.ManifestKey = "wrong" }, func(c *Current) { c.ManifestSHA256 = "invalid" },
	} {
		local, m, _ := fixture(t)
		c := publish(t, local, m, "")
		_, token, err := local.Current(t.Context(), m.Identity.CurrentKey())
		if err != nil {
			t.Fatal(err)
		}
		mutate(&c)
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err = local.CompareAndSwap(t.Context(), m.Identity.CurrentKey(), token, data); err != nil {
			t.Fatal(err)
		}
		if _, err = CurrentHead(t.Context(), local, m.Identity); err == nil {
			t.Fatal("corrupt current accepted")
		}
	}
}

// failureIsolationBackend exercises slow and failed per-object reads without
// affecting the archive binding or any healthy CURRENT.
type failureIsolationBackend struct {
	Backend
	bad   Identity
	cause error
	slow  bool
}

func (b *failureIsolationBackend) Current(ctx context.Context, key string) ([]byte, string, error) {
	if key == b.bad.CurrentKey() {
		if b.slow {
			<-ctx.Done()
			return nil, "", ctx.Err()
		}
		if b.cause != nil {
			return nil, "", b.cause
		}
	}
	return b.Backend.Current(ctx, key)
}

func TestCurrentHeadsFailuresAdvanceAndFullSweepRepairs(t *testing.T) {
	local, m, _ := fixture(t)
	a := m.Identity
	a.Agent = "claude"
	a.SessionID = "a"
	b := a
	b.SessionID = "b"
	for _, id := range []Identity{a, b} {
		c := Current{Contract: Contract, Identity: id, PublicationID: "pub", Sequence: 1, ManifestSHA256: strings.Repeat("a", 64)}
		c.ManifestKey = id.BaseKey() + "/manifests/" + c.ManifestSHA256 + ".json"
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err = local.CompareAndSwap(t.Context(), id.CurrentKey(), "", data); err != nil {
			t.Fatal(err)
		}
	}
	_, version, err := local.Current(t.Context(), a.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	if err = local.CompareAndSwap(t.Context(), a.CurrentKey(), version, []byte(`{"contract":"unsupported"}`)); err != nil {
		t.Fatal(err)
	}
	lister := &headTestBackend{Backend: local, prefixes: []string{a.BaseKey() + "/", b.BaseKey() + "/"}, done: false}
	page, err := ReadCurrentHeads(t.Context(), lister, "archive", "machine", "", 2)
	if err != nil || len(page.Failures) != 1 || page.Failures[0] != (PublicationHeadFailure{Identity: a, Reason: "current-unreadable"}) || len(page.Heads) != 1 || page.Heads[0].Identity != b {
		t.Fatal("A starved B", page, err)
	}
	cursor, err := parseHeadCursor("archive", "machine", page.NextCursor)
	if err != nil || cursor.After != b.BaseKey()+"/" {
		t.Fatal("failure did not advance", cursor, err)
	}
	// Follow through the empty remainder and all later agents.
	lister.prefixes = nil
	lister.done = true
	for _, agent := range []string{"claude", "codex", "pi"} {
		page, err = ReadCurrentHeads(t.Context(), lister, "archive", "machine", page.NextCursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if page.Complete != (agent == "pi") {
			t.Fatal("later agent blocked", page)
		}
	}
	// Repair A; a fresh sweep must retry it and remove the transient failure.
	_, version, err = local.Current(t.Context(), a.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	c := Current{Contract: Contract, Identity: a, PublicationID: "repaired", Sequence: 2, ManifestSHA256: strings.Repeat("a", 64)}
	c.ManifestKey = a.BaseKey() + "/manifests/" + c.ManifestSHA256 + ".json"
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = local.CompareAndSwap(t.Context(), a.CurrentKey(), version, data); err != nil {
		t.Fatal(err)
	}
	lister.prefixes = []string{a.BaseKey() + "/", b.BaseKey() + "/"}
	page, err = ReadCurrentHeads(t.Context(), lister, "archive", "machine", "", 2)
	if err != nil || len(page.Heads) != 2 || len(page.Failures) != 0 || page.Heads[0].PublicationID != "repaired" {
		t.Fatal("repair not retried", page, err)
	}
	for _, tc := range []struct {
		name  string
		cause error
		slow  bool
		fatal bool
	}{
		{name: "per-object503", cause: errors.New("private 503 response")},
		{name: "auth", cause: ErrDestinationPaused, fatal: true},
		{name: "slow", slow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			injected := &failureIsolationBackend{Backend: local, bad: a, cause: tc.cause, slow: tc.slow}
			lister.Backend = injected
			start := time.Now()
			page, err := ReadCurrentHeads(t.Context(), lister, "archive", "machine", "", 2)
			if tc.fatal {
				if !errors.Is(err, ErrDestinationPaused) {
					t.Fatal("auth did not fail endpoint", err)
				}
				return
			}
			if err != nil || len(page.Heads) != 1 || page.Heads[0].Identity != b || len(page.Failures) != 1 || page.Failures[0].Identity != a {
				t.Fatal("unreadable A blocked B", page, err)
			}
			if tc.slow && (time.Since(start) < 5*time.Second || time.Since(start) > 8*time.Second) {
				t.Fatal("CURRENT child budget not bounded", time.Since(start))
			}
		})
	}
	injected := &failureIsolationBackend{Backend: local, bad: a, slow: true}
	lister.Backend = injected
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = ReadCurrentHeads(ctx, lister, "archive", "machine", "", 2); !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation swallowed", err)
	}
	deadlineCtx, deadlineCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer deadlineCancel()
	if _, err = ReadCurrentHeads(deadlineCtx, lister, "archive", "machine", "", 2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("in-flight caller deadline swallowed", err)
	}
}
