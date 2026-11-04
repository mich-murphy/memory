package cli

import (
	"errors"
	"testing"
)

func TestPerAgentChecksOncePerAgent(t *testing.T) {
	calls := map[string]int{}
	boom := errors.New("paused")
	f := perAgent(func(a string) error {
		calls[a]++
		if a == "codex" {
			return boom
		}
		return nil
	})
	for range 3 {
		if f("claude") != nil || !errors.Is(f("codex"), boom) {
			t.Fatal("wrong memoized result")
		}
	}
	if calls["claude"] != 1 || calls["codex"] != 1 {
		t.Fatalf("calls = %v, want one per agent", calls)
	}
}

// A marker created between two Discover calls takes effect on the next call.
func TestUnresolvedNativeMarkerBetweenDiscoverCallsTakesEffect(t *testing.T) {
	c, config, _ := snapshotFixture(t)
	r, _, _ := countingComposition(t, c)
	discover, closeCursor := r.discovery(config)
	defer func() { _ = closeCursor() }()
	first, err := discover(t.Context())
	if err != nil || len(first.Requests) != snapshotFiles {
		t.Fatalf("first requests=%d err=%v", len(first.Requests), err)
	}
	if err := recordUnresolvedNativePolicy(t.Context(), c, "claude"); err != nil {
		t.Fatal(err)
	}
	second, err := discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Requests) != 0 {
		t.Fatalf("second call admitted %d requests despite the marker", len(second.Requests))
	}
}
