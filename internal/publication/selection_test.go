package publication

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func optionalFixture(t *testing.T) (*Local, Manifest, Source) {
	t.Helper()
	b, m, _ := fixture(t)
	s := m.Sources[0]
	s.SourceID = "PRIVATE-optional-source"
	s.Path = "/PRIVATE/path"
	s.Associations = []string{"PRIVATE-association"}
	s.Role = "artifact"
	s.Optional = true
	m.Sources = append(m.Sources, s)
	return b, m, s
}

func withheld(t *testing.T, s Source) Source {
	t.Helper()
	w, err := WithholdSource(s, "policy")
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestSelectionCommitmentAndRedaction(t *testing.T) {
	_, m, s := optionalFixture(t)
	w := withheld(t, s)
	// Independent field ordering oracle, not a call to the implementation helper.
	expected := token(fmt.Appendf(nil, "%s\x00%s\x00%s\x00%s\x00%d\x00%d", s.Object.Key, s.Object.SHA256, s.Object.EncodedSHA256, s.Object.Encoding, s.Object.Size, s.Object.EncodedSize))
	if ObjectCommitment(s.Object) != expected || w.Withheld.SnapshotCommitment != expected || SourceKeyHash(w) != token([]byte(s.SourceID)) || SourceKeyHash(s) != SourceKeyHash(w) {
		t.Fatal("commitment/identity mismatch")
	}
	if IsSelected(w) || !IsSelected(s) {
		t.Fatal("selection mismatch")
	}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE", s.Object.Key, s.Object.SHA256, s.Object.EncodedSHA256, "\"object\""} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("redaction leaked descriptor field %q", secret)
		}
	}
	if w.Object != (Object{}) || w.SourceID != "" || w.Path != "" || w.Associations != nil {
		t.Fatal("nonzero redacted source")
	}
	for _, reason := range []string{"policy", "integrity", "unavailable"} {
		changed, err := WithholdSource(w, reason)
		if err != nil || changed.Withheld.Reason != reason || w.Withheld.Reason != "policy" {
			t.Fatal("idempotent withholding mutated input", err)
		}
	}
	m.Sources[1] = w
	m.Coverage.Missing = append(m.Coverage.Missing, MissingSourceWithheld)
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
}

func TestWithheldValidationBranches(t *testing.T) {
	_, base, s := optionalFixture(t)
	w := withheld(t, s)
	tests := map[string]func(*Manifest){
		"required":                  func(m *Manifest) { m.Sources[1].Optional = false },
		"primary":                   func(m *Manifest) { m.Sources[1].Role = "transcript" },
		"source-leak":               func(m *Manifest) { m.Sources[1].SourceID = "PRIVATE" },
		"path-leak":                 func(m *Manifest) { m.Sources[1].Path = "PRIVATE" },
		"association-leak":          func(m *Manifest) { m.Sources[1].Associations = []string{"PRIVATE"} },
		"object-leak":               func(m *Manifest) { m.Sources[1].Object = s.Object },
		"hash":                      func(m *Manifest) { m.Sources[1].Withheld.SourceIDHash = "bad" },
		"commitment":                func(m *Manifest) { m.Sources[1].Withheld.SnapshotCommitment = strings.Repeat("A", 64) },
		"reason":                    func(m *Manifest) { m.Sources[1].Withheld.Reason = "PRIVATE" },
		"coverage-complete":         func(m *Manifest) { m.Coverage.Enrichment = "complete" },
		"coverage-unknown":          func(m *Manifest) { m.Coverage.Enrichment = "unknown" },
		"coverage-marker":           func(m *Manifest) { m.Coverage.Missing = nil },
		"duplicate-effective":       func(m *Manifest) { m.Sources = append(m.Sources, s) },
		"selected-primary-optional": func(m *Manifest) { m.Sources[0].Optional = true },
		"selected-no-object":        func(m *Manifest) { m.Sources[1] = s; m.Sources[1].Object = Object{} },
		"oversize":                  func(m *Manifest) { m.Sources[0].Path = strings.Repeat("x", MaxMetadata) },
		"nul-identity":              func(m *Manifest) { m.Identity.SessionID = "a\x00b" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			m := base
			m.Sources = append([]Source(nil), base.Sources...)
			m.Sources[1] = w
			copyW := *w.Withheld
			m.Sources[1].Withheld = &copyW
			m.Coverage.Missing = []string{MissingSourceWithheld}
			mutate(&m)
			if err := ValidateManifest(m); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*Source){
		"required": func(s *Source) { s.Optional = false }, "role": func(s *Source) { s.Role = "transcript" },
		"source": func(s *Source) { s.SourceID = "" }, "epoch": func(s *Source) { s.EpochID = "" },
		"revision": func(s *Source) { s.Revision = 0 }, "timestamp": func(s *Source) { s.CapturedAtNS = -1 },
		"object": func(s *Source) { s.Object = Object{} }, "nul": func(s *Source) { s.Object.Key = "a\x00b" },
		"invalid-withheld": func(s *Source) { *s = w; s.Optional = false },
	} {
		t.Run("helper-"+name, func(t *testing.T) {
			copyS := s
			mutate(&copyS)
			if _, err := WithholdSource(copyS, "policy"); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	if _, err := WithholdSource(s, "PRIVATE"); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("invalid reason or leaked error")
	}
}

func TestSelectedWithheldReselectedTransitions(t *testing.T) {
	b, m, s := optionalFixture(t)
	publish(t, b, m, "")
	_, tok, err := b.Current(t.Context(), m.Identity.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	m.PublicationID = "withheld"
	m.Sources[1] = withheld(t, s)
	m.Coverage.Missing = []string{MissingSourceWithheld}
	publish(t, b, m, tok)
	// A stable retry still verifies/fences only selected dependencies.
	publish(t, b, m, "obsolete-token")
	old := m
	for name, mutate := range map[string]func(*Manifest){
		"drop":    func(m *Manifest) { m.Sources = m.Sources[:1] },
		"regress": func(m *Manifest) { m.Sources[1].Revision = 0 },
		"commitment": func(m *Manifest) {
			w := *m.Sources[1].Withheld
			w.SnapshotCommitment = token([]byte("other"))
			m.Sources[1].Withheld = &w
		},
		"demote-primary": func(m *Manifest) { m.Sources[0].Role = "artifact"; m.Sources[0].Optional = true },
	} {
		t.Run(name, func(t *testing.T) {
			next := old
			next.Sequence++
			next.PublicationID = "invalid"
			next.Sources = append([]Source(nil), old.Sources...)
			mutate(&next)
			if err := advances(old, next); !errors.Is(err, ErrConflict) {
				t.Fatalf("regression accepted: %v", err)
			}
		})
	}
	_, tok, err = b.Current(t.Context(), m.Identity.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	m.PublicationID = "reselected"
	m.Sources[1] = s
	publish(t, b, m, tok)
	out, err := ExportSession(t.Context(), b, m.Identity, filepath.Join(t.TempDir(), "export"))
	if err != nil || len(out.Bodies) != 2 {
		t.Fatal("reselection did not restore closure", err)
	}
	// Required and selected optional objects both remain real dependencies.
	if err := os.Remove(filepath.Join(b.root, "data", s.Object.Key)); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(t.Context(), b, m.Identity); err == nil {
		t.Fatal("missing selected dependency accepted")
	}
}

func TestIndependentWithheldOnlyReader(t *testing.T) {
	b, m, s := optionalFixture(t)
	m.Sources = []Source{withheld(t, s)}
	m.Coverage.Missing = []string{MissingSourceWithheld}
	// Remove all bodies before publishing: withholding cannot touch dependencies.
	if err := os.Remove(filepath.Join(b.root, "data", s.Object.Key)); err != nil {
		t.Fatal(err)
	}
	publish(t, b, m, "")
	reopened := NewLocal(b.root)
	if _, err := Read(t.Context(), reopened, m.Identity); err != nil {
		t.Fatal(err)
	}
	out, err := ExportSession(t.Context(), reopened, m.Identity, filepath.Join(t.TempDir(), "export"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Bodies) != 0 || len(out.Manifest.Sources) != 1 || out.Manifest.Sources[0].Withheld == nil {
		t.Fatal("withheld coordinate lost or body fabricated")
	}
}

func TestV1ContractsAreExplicitlyUnsupported(t *testing.T) {
	b, m, _ := fixture(t)
	if Contract != "session-publication/v2" || ReaderContract != "session-reader/v2" || Prefix != "capture-v1" {
		t.Fatal("wire version/namespace mismatch")
	}
	m.Contract = "session-publication/v1"
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
	if err := WriteArchive(t.Context(), b, Archive{Contract: "session-publication/v1", ArchiveID: m.Identity.ArchiveID}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
}

func TestWithheldCoordinatesCountTowardLimits(t *testing.T) {
	_, m, s := optionalFixture(t)
	w := withheld(t, s)
	m.Coverage.Missing = []string{MissingSourceWithheld}
	m.Sources = make([]Source, MaxSources)
	for n := range m.Sources {
		m.Sources[n] = w
		m.Sources[n].EpochID = fmt.Sprintf("epoch-%d", n)
	}
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
	m.Sources = append(m.Sources, w)
	if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "source limit") {
		t.Fatal(err)
	}
	w.EpochID = ""
	if _, err := WithholdSource(w, "policy"); err == nil {
		t.Fatal("invalid retained epoch accepted")
	}
	var a Archive
	if err := decode([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'}, &a); err == nil {
		t.Fatal("invalid UTF-8 metadata accepted")
	}
}

func TestOldBoundArchiveIsNotReset(t *testing.T) {
	b := NewLocal(t.TempDir())
	old := []byte(`{"contract":"session-publication/v1","archive_id":"old-archive"}`)
	if err := b.CompareAndSwap(t.Context(), ArchiveKey, "", old); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchive(t.Context(), b); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
	if err := WriteArchive(t.Context(), b, Archive{Contract: Contract, ArchiveID: "new-archive"}); err == nil {
		t.Fatal("old bound store reset")
	}
	got, _, err := b.Current(t.Context(), ArchiveKey)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatal("old binding mutated", err)
	}
}
