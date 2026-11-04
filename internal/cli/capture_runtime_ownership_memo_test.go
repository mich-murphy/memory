package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Errors are remembered as errors (never success) and per full key.
func TestOwnershipMemoRemembersErrorsAndKeysFully(t *testing.T) {
	reads := 0
	m := newOwnershipMemo(func(_ context.Context, _, root, uuid string) (string, string, error) {
		reads++
		if uuid == "u1" {
			return "", "", sessioncapture.ErrOwnershipPending
		}
		return "s-" + root + uuid, "l", nil
	})
	for range 2 {
		if _, _, err := m.lookup(t.Context(), "claude", "/r", "u1"); !errors.Is(err, sessioncapture.ErrOwnershipPending) {
			t.Fatalf("err = %v, want pending", err)
		}
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
	s1, _, _ := m.lookup(t.Context(), "claude", "/r1", "u2")
	s2, _, _ := m.lookup(t.Context(), "claude", "/r2", "u2")
	s3, _, _ := m.lookup(t.Context(), "claude", "/r1", "u3")
	if s1 != "s-/r1u2" || s2 != "s-/r2u2" || s3 != "s-/r1u3" {
		t.Fatalf("keys collided: %q %q %q", s1, s2, s3)
	}
	// A new memo (next call) re-reads.
	m2 := newOwnershipMemo(m.read)
	before := reads
	if _, _, err := m2.lookup(t.Context(), "claude", "/r", "u1"); err == nil || reads != before+1 {
		t.Fatal("new memo must read afresh")
	}
}

func TestOwnershipMemoDoesNotRememberCancelledLookup(t *testing.T) {
	reads := 0
	m := newOwnershipMemo(func(context.Context, string, string, string) (string, string, error) {
		reads++
		return "", "", context.Canceled
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, _ = m.lookup(ctx, "claude", "/r", "u")
	_, _, _ = m.lookup(ctx, "claude", "/r", "u")
	if reads != 2 {
		t.Fatalf("reads = %d, want 2 (cancelled not remembered)", reads)
	}
}
