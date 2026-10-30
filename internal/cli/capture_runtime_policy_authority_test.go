package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestRuntimeMissingPolicyAuthorityStopsRunBeforeCapture(t *testing.T) {
	c, _ := runtimeFixture(t)
	path := filepath.Join(c.Home, ".claude", "projects", "synthetic", "a.jsonl")
	runtimeWrite(t, path)
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	req, err := capturesource.Classify(t.Context(), r.sourceOptions(""), "claude", path)
	if err != nil {
		t.Fatal(err)
	}
	q, err := sessioncapture.Open(t.Context(), c.Spool, r.queueOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	backend := publication.NewLocal(c.Store)
	if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	// Source remains available; only the policy authority disappears. The
	// coordinator must not silently treat the failure as a retryable source miss.
	if err := os.Rename(c.PolicyDir, c.PolicyDir+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = q.Run(ctx, backend, sessioncapture.RuntimeOptions{PollInterval: 10 * time.Millisecond, OperationTimeout: time.Second, CaptureWorkers: 1, DeliveryWorkers: 1})
	// The queue intentionally sanitizes callback causes at its trust boundary;
	// the fatal storage marker, not the private callback cause, reaches Run.
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "policy storage unavailable") {
		t.Fatalf("policy authority did not stop runtime: %v", err)
	}
	if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), c.PolicyDir) {
		t.Fatal("private path exposed in policy failure")
	}
	rows, err := q.WorkStatus(t.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CapturedVersion != 0 {
		t.Fatalf("source captured without policy authority: %#v", rows)
	}
	if _, _, err := backend.Current(context.WithoutCancel(t.Context()), req.Identity.CurrentKey()); !errors.Is(err, publication.ErrNotFound) {
		t.Fatalf("source published without policy authority: %v", err)
	}
}

func TestRuntimeRegistryFailureIsFatalButDenialIsNot(t *testing.T) {
	c, _ := runtimeFixture(t)
	path := filepath.Join(c.Home, ".claude", "projects", "synthetic", "a.jsonl")
	runtimeWrite(t, path)
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	req, err := capturesource.Classify(t.Context(), r.sourceOptions(""), "claude", path)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(c.PolicyDir, "exclusions", "bad.json")
	if err := os.WriteFile(bad, []byte("synthetic-private-invalid-registry"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = r.policy(false)(t.Context(), req)
	if !errors.Is(err, sessioncapture.ErrPolicyUnavailable) || strings.Contains(err.Error(), "synthetic-private") || strings.Contains(err.Error(), bad) {
		t.Fatalf("unsafe registry diagnostic: %v", err)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	req.Path = filepath.Join(c.Home, "unrelated.jsonl")
	if err := r.policy(false)(t.Context(), req); err == nil || errors.Is(err, sessioncapture.ErrPolicyUnavailable) {
		t.Fatalf("ordinary source denial upgraded to fatal: %v", err)
	}
}
