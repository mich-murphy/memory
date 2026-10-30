package publication

import (
	"math"
	"strings"
	"testing"
)

func TestWriterRejectsLossyJSONIdentities(t *testing.T) {
	_, original, _ := fixture(t)
	bad := string([]byte{0xff})
	for name, mutate := range map[string]func(*Manifest){
		"sequence-overflow": func(m *Manifest) { m.Sequence = math.MaxInt64 + 1 },
		"revision-overflow": func(m *Manifest) { m.Sources[0].Revision = math.MaxInt64 + 1 },
		"archive":           func(m *Manifest) { m.Identity.ArchiveID = bad },
		"machine":           func(m *Manifest) { m.Identity.MachineID = bad },
		"session":           func(m *Manifest) { m.Identity.SessionID = bad },
		"source":            func(m *Manifest) { m.Sources[0].SourceID = bad },
		"epoch":             func(m *Manifest) { m.Sources[0].EpochID = bad },
		"path":              func(m *Manifest) { m.Sources[0].Path = bad },
		"association":       func(m *Manifest) { m.Sources[0].Associations = []string{bad} },
		"coverage":          func(m *Manifest) { m.Coverage.Missing = []string{bad} },
	} {
		t.Run(name, func(t *testing.T) {
			m := original
			m.Sources = append([]Source(nil), original.Sources...)
			mutate(&m)
			if err := ValidateManifest(m); err == nil {
				t.Fatal("lossy identity or out-of-range consumer coordinate must not be accepted")
			}
		})
	}
	backend := NewLocal(t.TempDir())
	if err := WriteArchive(t.Context(), backend, Archive{Contract: Contract, ArchiveID: bad}); err == nil {
		t.Fatal("invalid archive identity accepted")
	}
	private := "PRIVATE-unsupported-contract-value"
	if err := WriteArchive(t.Context(), backend, Archive{Contract: private, ArchiveID: "archive"}); err == nil || strings.Contains(err.Error(), private) {
		t.Fatal("unsupported contract must fail without echoing untrusted metadata")
	}
}
