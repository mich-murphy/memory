package capturesource

import (
	"context"
	"testing"
)

// A decision reached under a cancelled context is not remembered; a live one is.
func TestAllowMemoDoesNotRememberCancelledDecision(t *testing.T) {
	m := newAllowMemo()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	k := allowKey{Root{"claude", "/r"}, "/r/p"}
	calls := 0
	eval := func() bool { calls++; return false }
	m.decide(ctx, k, eval)
	m.decide(ctx, k, eval)
	if calls != 2 {
		t.Fatalf("cancelled evaluations = %d, want 2 (not remembered)", calls)
	}
	m.decide(t.Context(), k, eval)
	m.decide(t.Context(), k, eval)
	if calls != 3 {
		t.Fatalf("live evaluations total = %d, want 3 (second remembered)", calls)
	}
}

// Distinct paths under one root are distinct decisions.
func TestAllowMemoKeysOnRootAndPath(t *testing.T) {
	m := newAllowMemo()
	a, b := allowKey{Root{"claude", "/r"}, "/r/a"}, allowKey{Root{"claude", "/r"}, "/r/b"}
	if !m.decide(t.Context(), a, func() bool { return true }) || m.decide(t.Context(), b, func() bool { return false }) {
		t.Fatal("decision for b leaked from a")
	}
}
