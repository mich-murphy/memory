package sessioncapture

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrichmentAdmissionRepairAndReadOnlyStatus(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=0 WHERE name='enrichment'`); err != nil {
		t.Fatal(err)
	}
	m := capture(t, q, r)
	if err := q.retryEnrichmentAdmission(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=? WHERE name='enrichment'`, MaxEnrichmentMetadataBytes); err != nil {
		t.Fatal(err)
	}
	if err := q.retryEnrichmentAdmission(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.retryEnrichmentAdmission(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := ReadEnrichmentWorkStatus(t.Context(), dir, o.ArchiveID, "", 20)
	if err != nil || len(status) != 1 {
		t.Fatalf("read-only status %d: %v", len(status), err)
	}
	if status[0].Spec.BasisPublication != m.PublicationID || !status[0].BasisCurrent {
		t.Fatal("admission lost exact basis")
	}
	if _, err := ReadEnrichmentWorkStatus(t.Context(), dir, "wrong", "", 20); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	if _, err := ReadEnrichmentWorkStatus(t.Context(), filepath.Join(dir, "absent"), o.ArchiveID, "", 20); err == nil {
		t.Fatal("missing store accepted")
	}
	enrichOnce(t, q, snapshotOptions("result"))
	status, err = q.EnrichmentWorkStatus(t.Context(), "", 20)
	if err != nil || !status[0].Complete || status[0].ResultID == "" {
		t.Fatalf("missing completeness: %v", err)
	}
	if err := q.ReplayEnrichment(t.Context(), "absent"); err == nil {
		t.Fatal("unknown replay accepted")
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE enrichment_work SET state='working'`); err != nil {
		t.Fatal(err)
	}
	if err := q.ReplayEnrichment(t.Context(), status[0].ID); err == nil {
		t.Fatal("working replay accepted")
	}
}

func TestEnrichmentAssetsClosedBoundsAndAuthorization(t *testing.T) {
	valid := EnrichmentAsset{Path: "/asset", Identities: []string{"/original"}, Kind: "file", Scope: "project", Selector: "declared"}
	opts := snapshotOptions("body")
	opts.CheckEnrichmentAsset = func(context.Context, EnrichmentSpec, EnrichmentAsset, bool) error { return nil }
	if err := checkAssets(t.Context(), EnrichmentSpec{}, []EnrichmentAsset{valid}, true, opts); err != nil {
		t.Fatal(err)
	}
	if err := checkAssets(t.Context(), EnrichmentSpec{}, []EnrichmentAsset{valid}, true, RuntimeOptions{}); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	opts.CheckEnrichmentAsset = func(context.Context, EnrichmentSpec, EnrichmentAsset, bool) error { return errors.New("private error") }
	if err := checkAssets(t.Context(), EnrichmentSpec{}, []EnrichmentAsset{valid}, false, opts); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := checkAssets(ctx, EnrichmentSpec{}, []EnrichmentAsset{valid}, false, opts); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, a := range []EnrichmentAsset{{Path: "\x00"}, {Scope: "\n"}, {Selector: string([]byte{255})}, {Identities: make([]string, 65)}, {Identities: []string{"\x00"}}} {
		if err := validateEnrichmentAsset(a); !errors.Is(err, ErrPolicy) {
			t.Fatal(err)
		}
	}
	if err := checkAssets(t.Context(), EnrichmentSpec{}, make([]EnrichmentAsset, MaxEnrichmentAssets+1), false, opts); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
}

func TestNativeEvidenceStorageValidationAndImmutableConflicts(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	valid := NativeEvidence{Contract: EnrichmentContract, ID: "launch", Identity: r.Identity, Request: r, ObservedAtNS: 1, Context: NativeContext{Argv: []string{"claude", "--model", "model-1", "private prompt"}, Environment: map[string]string{"ANTHROPIC_MODEL": "model-1", "SECRET_KEY": "private"}}}
	body, err := validateNative(valid)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private") {
		t.Fatal("native secret retained")
	}
	for _, change := range []func(*NativeEvidence){func(n *NativeEvidence) { n.Contract = "unknown" }, func(n *NativeEvidence) { n.ID = "" }, func(n *NativeEvidence) { n.ObservedAtNS = -1 }, func(n *NativeEvidence) { n.Identity.SessionID = "other" }, func(n *NativeEvidence) { n.Request.Role = "unknown" }, func(n *NativeEvidence) { n.Context.Model = "\x00" }, func(n *NativeEvidence) { n.Context.Missing = make([]string, 65) }, func(n *NativeEvidence) { n.Context.Argv = make([]string, 33) }, func(n *NativeEvidence) { n.Context.Environment = map[string]string{"KEY": "\n"} }} {
		n := valid
		change(&n)
		if _, err := validateNative(n); err == nil {
			t.Fatal("invalid native accepted")
		}
	}
	if err := WriteNativeEvidence(t.Context(), dir, valid); err != nil {
		t.Fatal(err)
	}
	conflict := valid
	conflict.Context.Model = "different"
	if err := WriteNativeEvidence(t.Context(), dir, conflict); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	if err := q.AcceptNativeEvidence(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=used WHERE name='native'`); err != nil {
		t.Fatal(err)
	}
	other := valid
	other.ID = "other"
	if err := q.AcceptNativeEvidence(t.Context(), other); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	if err := q.AcceptNativeEvidence(t.Context(), valid); err != nil {
		t.Fatal("exact retry at quota", err)
	}
}

func TestEnrichmentSpecValidation(t *testing.T) {
	_, _, r := setup(t)
	valid := EnrichmentSpec{Kind: "snapshot", Identity: r.Identity, BasisPublication: "publication", BasisSequence: 1, Source: SourceCoordinate{SourceID: r.SourceID, Path: r.Path, EpochID: "epoch", Revision: 1, SHA256: strings.Repeat("a", 64)}, SelectionVersion: "v1"}
	if _, err := validateSpec(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*EnrichmentSpec){func(s *EnrichmentSpec) { s.Kind = "unknown" }, func(s *EnrichmentSpec) { s.Identity.Agent = "" }, func(s *EnrichmentSpec) { s.SelectionVersion = "" }, func(s *EnrichmentSpec) { s.Source.Revision = 0 }, func(s *EnrichmentSpec) { s.Kind = "launch" }, func(s *EnrichmentSpec) { s.Root = "\x00" }} {
		s := valid
		change(&s)
		if _, err := validateSpec(s); err == nil {
			t.Fatal("invalid spec accepted")
		}
	}
	valid.Kind = "launch"
	valid.LaunchID = "launch"
	if _, err := validateSpec(valid); err != nil {
		t.Fatal(err)
	}
}

func TestEnrichmentBuilderErrorsAndScopeDenial(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	spec := EnrichmentSpec{Kind: "snapshot", Identity: r.Identity}
	for _, fn := range []EnrichFunc{nil, func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{}, io.ErrUnexpectedEOF
	}, func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) { return EnrichmentArtifact{}, nil }, func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{Body: io.NopCloser(strings.NewReader("body")), ContentHash: "mismatch"}, nil
	}, func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{Body: io.NopCloser(strings.NewReader("body")), Missing: make([]string, 129)}, nil
	}} {
		opts := runtimeDefaults(RuntimeOptions{Enrich: fn})
		if _, err := q.buildEnrichment(t.Context(), spec, opts); err == nil {
			t.Fatal("build accepted failure")
		}
	}
	denied := false
	q.opts.CheckPolicy = func(context.Context, Request) error {
		if denied {
			return errors.Join(ErrSessionExcluded, io.ErrUnexpectedEOF)
		}
		return nil
	}
	// An optional-only session is still covered by the whole-session exclusion.
	r.Optional = true
	r.Role = "artifact"
	capture(t, q, r)
	denied = true
	if _, err := q.RefreshOptionalSelection(t.Context(), r.Identity); !errors.Is(err, ErrSessionExcluded) || !errors.Is(err, ErrPolicy) {
		t.Fatalf("scope was withheld: %v", err)
	}
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); !errors.Is(err, ErrSessionExcluded) {
		t.Fatal(err)
	}
	denied = false
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET next_retry=0`); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.Path); err != nil {
		t.Fatal(err)
	}
}
