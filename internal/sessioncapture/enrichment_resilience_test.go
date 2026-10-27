package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNativePoisonPageDoesNotStarveLaterEvidence(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	inbox, lock, err := lockNativeInbox(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for n := range 40 {
		if err := os.WriteFile(filepath.Join(inbox, fmt.Sprintf("000-poison-%02d", n)), []byte("not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	record := NativeEvidence{Contract: EnrichmentContract, ID: "launch", Identity: r.Identity, Request: r, ObservedAtNS: 1}
	if err := WriteNativeEvidence(t.Context(), dir, record); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := q.drainNativeEvidence(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM native_evidence`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("poison starvation %d %v", count, err)
	}
	entries, err := nativeEntries(inbox)
	if err != nil || len(entries) != 40 {
		t.Fatal("poison records not retained", err)
	}
}

func TestEnrichmentBudgetExhaustionIsDurableNotRuntimeFatal(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	capture(t, q, r)
	if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=used WHERE name='enrichment'`); err != nil {
		t.Fatal(err)
	}
	opts := runtimeDefaults(RuntimeOptions{Enrich: func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{}, io.ErrUnexpectedEOF
	}})
	enrichOnce(t, q, opts)
	status, err := q.EnrichmentWorkStatus(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if status[0].State != "retry" || status[0].LastError == "" || status[0].Attempts != 1 {
		t.Fatal("budget failure not durably visible")
	}
	write(t, r.Path, "first\nmore\n")
	capture(t, q, r)
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); err != nil {
		t.Fatal("enrichment budget blocked raw", err)
	}
}

func TestOptionalRepairFailureRetainedAndDestinationNotWithheld(t *testing.T) {
	q, r, m := optionalFixture(t)
	backend := &optionalBackend{Backend: newBackend(t), key: m.Sources[1].Object.Key, stage: "upload"}
	backend.fail.Store(true)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	did, err := q.optionalRepairNext(t.Context(), backend, runtimeDefaults(RuntimeOptions{MaxAttempts: 1}))
	if err != nil || !did {
		t.Fatalf("repair failure %v %v", did, err)
	}
	var state string
	if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM optional_repairs`).Scan(&state); err != nil || state != "quarantined" {
		t.Fatal("repair failure not retained", err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE optional_repairs SET state='pending',next_retry=0`); err != nil {
		t.Fatal(err)
	}
	backend.fail.Store(false)
	if _, err := q.optionalRepairNext(t.Context(), backend, runtimeDefaults(RuntimeOptions{})); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	if errors.Is(err, ErrDestination) {
		t.Fatal("unexpected destination classification")
	}
}
