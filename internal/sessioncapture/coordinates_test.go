package sessioncapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/publication"
)

func aliasQueueFixture(t *testing.T) (Options, Request, string, string, string) {
	t.Helper()
	_, o, _ := setup(t)
	base := t.TempDir()
	logicalRoot := filepath.Join(base, "logical")
	a := filepath.Join(base, "physical-a")
	b := filepath.Join(base, "physical-b")
	r := ownerRequest(t, logicalRoot, "project", true)
	logical := r.Path
	rel, err := filepath.Rel(logicalRoot, logical)
	if err != nil {
		t.Fatal(err)
	}
	pa, pb := filepath.Join(a, rel), filepath.Join(b, rel)
	hint := filepath.Join(base, "original-hint")
	for _, path := range []string{pa, pb} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, pa, "first\n")
	if err := os.Remove(logical); err != nil {
		t.Fatal(err)
	}
	inventory, err := capturecoordinates.Compile([]capturecoordinates.Root{{Agent: "claude", Path: logicalRoot}}, []capturecoordinates.RootAlias{{Agent: "claude", Root: logicalRoot, Path: a}, {Agent: "claude", Root: logicalRoot, Path: b}}, []capturecoordinates.LeafAlias{{Agent: "claude", LogicalPath: logical, Path: hint}})
	if err != nil {
		t.Fatal(err)
	}
	o.Coordinates = inventory
	r.Path = pa
	r.LogicalPath = logical
	r.Identities = []string{hint}
	return o, r, pa, pb, hint
}

func TestAliasCaptureAppendRewriteAndCanonicalOwnership(t *testing.T) {
	o, r, a, b, hint := aliasQueueFixture(t)
	q := open(t, filepath.Join(t.TempDir(), "spool"), o)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	first := capture(t, q, r)
	if first.Sources[0].Path != r.LogicalPath {
		t.Fatal("public path not logical")
	}
	write(t, b, "first\nsecond\n")
	r.Path = b
	if err := q.RecordOwnership(t.Context(), r); err != nil {
		t.Fatal("second physical route created ownership ambiguity", err)
	}
	next := capture(t, q, r)
	if len(next.Sources) != 1 || next.Sources[0].EpochID != first.Sources[0].EpochID || next.Sources[0].Revision != 2 {
		t.Fatal("physical append changed logical epoch")
	}
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{a, b, hint, r.LogicalPath} {
		if !slices.Contains(es[0].Identities, path) {
			t.Fatalf("lost private alias identity %q", path)
		}
	}
	var count int
	var path string
	if err := q.db.QueryRowContext(t.Context(), `SELECT count(*),min(main_path) FROM source_ownership`).Scan(&count, &path); err != nil || count != 1 || path != r.LogicalPath {
		t.Fatal("ownership did not canonicalize", count, path, err)
	}
	write(t, b, "rewritten\n")
	rewritten := capture(t, q, r)
	if len(rewritten.Sources) != 2 || rewritten.Sources[0].EpochID != first.Sources[0].EpochID || rewritten.Sources[0].State != "sealed" {
		t.Fatal("alias rewrite discarded old epoch")
	}
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "export")
	out, err := publication.ExportSession(t.Context(), backend, r.Identity, dest)
	if err != nil || len(out.Bodies) != 2 {
		t.Fatal("alias writer-reader coverage", err)
	}
	bodies := []string{}
	for _, body := range out.Bodies {
		data, err := os.ReadFile(filepath.Join(dest, body.Path))
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(data))
	}
	if !slices.Contains(bodies, "first\nsecond\n") || !slices.Contains(bodies, "rewritten\n") {
		t.Fatal("alias epochs lost exact bytes")
	}
}

func TestAbsentOldAliasOptoutBlocksCaptureAndDelivery(t *testing.T) {
	o, r, a, b, _ := aliasQueueFixture(t)
	denied := false
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if denied && req.Path == a {
			return ErrSessionExcluded
		}
		return nil
	}
	q := open(t, filepath.Join(t.TempDir(), "spool"), o)
	capture(t, q, r)
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	previous, err := publication.Read(t.Context(), backend, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	r.Path = b
	write(t, b, "first\nnew\n")
	capture(t, q, r)
	denied = true
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrSessionExcluded) {
		t.Fatal("absent alias optout bypassed capture", err)
	}
	if err := q.Deliver(t.Context(), backend, r.Identity); !errors.Is(err, ErrSessionExcluded) {
		t.Fatal("absent alias optout bypassed delivery", err)
	}
	after, err := publication.Read(t.Context(), backend, r.Identity)
	if err != nil || after.PublicationID != previous.PublicationID {
		t.Fatal("denial advanced remote root", err)
	}
}

func TestAliasProvenanceNeverGrantsAuthorityAndIsBounded(t *testing.T) {
	o, r, _, _, _ := aliasQueueFixture(t)
	q := open(t, filepath.Join(t.TempDir(), "spool"), o)
	forged := r
	forged.LogicalPath = filepath.Join(filepath.Dir(r.LogicalPath), "other.jsonl")
	if _, err := q.Capture(t.Context(), forged); !errors.Is(err, ErrPolicy) {
		t.Fatal("forged logical path authorized", err)
	}
	if err := q.Hint(t.Context(), forged); !errors.Is(err, ErrPolicy) {
		t.Fatal("forged hint authorized", err)
	}
	if err := q.RecordOwnership(t.Context(), forged); !errors.Is(err, ErrPolicy) {
		t.Fatal("forged ownership authorized", err)
	}
	plain := o
	plain.Coordinates = nil
	without := open(t, filepath.Join(t.TempDir(), "spool"), plain)
	if err := without.RecordOwnership(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatal("request metadata granted inventory authority", err)
	}
	for _, bad := range []Request{
		{LogicalPath: "relative"}, {Identities: []string{"/bad/../path"}}, {Identities: []string{"/nul\x00path"}}, {Identities: make([]string, MaxRequestIdentities+1)},
	} {
		test := r
		test.LogicalPath = bad.LogicalPath
		test.Identities = bad.Identities
		if err := validateHint(test); err == nil {
			t.Fatal("invalid provenance accepted at inbox boundary")
		}
	}
	many := r
	many.Identities = make([]string, MaxRequestIdentities)
	for n := range many.Identities {
		many.Identities[n] = "/" + strings.Repeat("x", 1024)
	}
	if err := validateHint(many); !errors.Is(err, ErrQuota) {
		t.Fatal("identity byte cap bypassed", err)
	}
	// Invented restrictive identities cannot select bytes or an owner coordinate.
	restricted := r
	extra := filepath.Join(t.TempDir(), "additional-private-identity")
	restricted.Identities = append(slices.Clone(r.Identities), extra)
	q.opts.CheckPolicy = func(_ context.Context, req Request) error {
		if req.Path == extra {
			return ErrSessionExcluded
		}
		return nil
	}
	if _, err := q.Capture(t.Context(), restricted); !errors.Is(err, ErrSessionExcluded) {
		t.Fatal("additional privacy identity ignored", err)
	}
}

func TestAliasNativeImmutableMetadataAndRestrictiveDelivery(t *testing.T) {
	o, r, _, _, _ := aliasQueueFixture(t)
	o.EnableEnrichment = true
	extra := filepath.Join(t.TempDir(), "original-private-hint")
	r.Identities = append(slices.Clone(r.Identities), extra)
	denied := false
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if denied && req.Path == extra {
			return ErrSessionExcluded
		}
		return nil
	}
	spool := filepath.Join(t.TempDir(), "spool")
	q := open(t, spool, o)
	record := NativeEvidence{Contract: EnrichmentContract, ID: "alias-launch", Identity: r.Identity, Request: r, ObservedAtNS: 1, Context: NativeContext{Event: "start", ContextOrigin: "native"}}
	if err := WriteNativeEvidence(t.Context(), spool, record); err != nil {
		t.Fatal(err)
	}
	if err := q.drainNativeEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	changed := record
	changed.Request.Identities = nil
	if err := q.AcceptNativeEvidence(t.Context(), changed); !errors.Is(err, ErrIntegrity) {
		t.Fatal("native provenance mutation accepted", err)
	}
	// Native metadata acceptance did not capture raw source bytes.
	var jobs int
	if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("native hint bypassed capture", jobs, err)
	}
	enrichOnce(t, q, snapshotOptions("bundle"))
	enrichOnce(t, q, snapshotOptions("bundle"))
	m, es, err := q.latest(t.Context(), r.Identity)
	if err != nil || len(m.Sources) != 2 {
		t.Fatal("native attachment", err)
	}
	for n := range es {
		if !slices.Contains(es[n].Request.Identities, extra) {
			t.Fatal("native artifact lost original restrictive provenance")
		}
	}
	denied = true
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); !errors.Is(err, ErrSessionExcluded) {
		t.Fatal("native optional delivery bypassed restrictive identity", err)
	}
	// Scope denial must remain fatal even if optional selection was already withheld.
	for n := range m.Sources {
		m.Sources[n], err = publication.WithholdSource(m.Sources[n], "policy")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := q.recheckScopes(t.Context(), es); !errors.Is(err, ErrSessionExcluded) {
		t.Fatal("withheld native scope bypassed original identity", err)
	}
	forged := record
	forged.ID = "forged"
	forged.Request.LogicalPath = filepath.Join(filepath.Dir(r.LogicalPath), "forged.jsonl")
	denied = false
	if err := q.AcceptNativeEvidence(t.Context(), forged); !errors.Is(err, ErrPolicy) {
		t.Fatal("forged native logical provenance authorized", err)
	}
	tooMany := record
	tooMany.Request.Identities = make([]string, MaxRequestIdentities+1)
	if err := WriteNativeEvidence(t.Context(), spool, tooMany); !errors.Is(err, ErrQuota) {
		t.Fatal("native provenance cap bypassed", err)
	}
}

func TestCoordinatesDoNotRejectUnmappedPrivateEnrichment(t *testing.T) {
	o, r, _, _, _ := aliasQueueFixture(t)
	o.EnableEnrichment = true
	q := open(t, filepath.Join(t.TempDir(), "spool"), o)
	capture(t, q, r)
	enrichOnce(t, q, snapshotOptions("optional bundle"))
	// The private prepared-path request used by materialization is outside all
	// source inventory roots. Coordinates do not broaden or replace that authority.
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); err != nil {
		t.Fatal("inventory rejected existing enrichment authority", err)
	}
}

func TestInventoryOwnershipRejectsUnmappedStructuralClaim(t *testing.T) {
	o, valid, _, _, _ := aliasQueueFixture(t)
	q := open(t, filepath.Join(t.TempDir(), "spool"), o)
	if err := q.RecordOwnership(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	outside := ownerRequest(t, filepath.Join(t.TempDir(), "unmapped"), "project", true)
	if err := q.RecordOwnership(t.Context(), outside); !errors.Is(err, ErrPolicy) {
		t.Fatal("inventory ownership accepted undeclared root", err)
	}
	var count int
	if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM source_ownership`).Scan(&count); err != nil || count != 1 {
		t.Fatal("forged root changed ownership index", count, err)
	}
}
