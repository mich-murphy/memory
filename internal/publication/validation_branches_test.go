package publication

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestValidationBranches(t *testing.T) {
	_, original, _ := fixture(t)
	bad := string([]byte{0xff})
	tests := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"contract", func(m *Manifest) { m.Contract = "private-unsupported" }, "unsupported manifest contract"},
		{"empty-archive", func(m *Manifest) { m.Identity.ArchiveID = "" }, "invalid manifest identity or sequence"},
		{"empty-machine", func(m *Manifest) { m.Identity.MachineID = "" }, "invalid manifest identity or sequence"},
		{"empty-agent", func(m *Manifest) { m.Identity.Agent = "" }, "invalid manifest identity or sequence"},
		{"empty-session", func(m *Manifest) { m.Identity.SessionID = "" }, "invalid manifest identity or sequence"},
		{"invalid-agent", func(m *Manifest) { m.Identity.Agent = bad }, "invalid manifest identity or sequence"},
		{"empty-publication", func(m *Manifest) { m.PublicationID = "" }, "invalid manifest identity or sequence"},
		{"invalid-publication", func(m *Manifest) { m.PublicationID = bad }, "invalid manifest identity or sequence"},
		{"zero-sequence", func(m *Manifest) { m.Sequence = 0 }, "invalid manifest identity or sequence"},
		{"overflow-sequence", func(m *Manifest) { m.Sequence = math.MaxInt64 + 1 }, "invalid manifest identity or sequence"},
		{"observed", func(m *Manifest) { m.ObservedAtNS = -1 }, "negative manifest timestamp"},
		{"captured", func(m *Manifest) { m.CapturedAtNS = -1 }, "negative manifest timestamp"},
		{"published", func(m *Manifest) { m.PublishedAtNS = -1 }, "negative manifest timestamp"},
		{"source-limit", func(m *Manifest) { m.Sources = make([]Source, MaxSources+1) }, "source limit exceeded"},
		{"empty-discovery", func(m *Manifest) { m.Coverage.Discovery = "" }, "coverage required"},
		{"empty-enrichment", func(m *Manifest) { m.Coverage.Enrichment = "" }, "coverage required"},
		{"invalid-discovery", func(m *Manifest) { m.Coverage.Discovery = bad }, "coverage required"},
		{"invalid-enrichment", func(m *Manifest) { m.Coverage.Enrichment = bad }, "coverage required"},
		{"invalid-missing", func(m *Manifest) { m.Coverage.Missing = []string{bad} }, "coverage required"},
		{"duplicate-pair", func(m *Manifest) { m.Sources = append(m.Sources, m.Sources[0]) }, "invalid or duplicate source epoch"},
	}
	for _, field := range []string{"source", "epoch", "role", "state", "path", "association", "revision", "observed", "captured", "size", "encoded-size", "encoding", "digest", "encoded-digest", "key"} {
		variants := []string{"empty", "invalid"}
		switch field {
		case "observed", "captured", "key", "encoding":
			variants = []string{"invalid"}
		}
		for _, variant := range variants {
			tests = append(tests, struct {
				name   string
				mutate func(*Manifest)
				want   string
			}{field + "-" + variant, func(m *Manifest) {
				s := &m.Sources[0]
				value := ""
				if variant == "invalid" {
					value = bad
				}
				switch field {
				case "source":
					s.SourceID = value
				case "epoch":
					s.EpochID = value
				case "role":
					s.Role = value
				case "state":
					s.State = value
				case "path":
					s.Path = bad
				case "association":
					s.Associations = []string{bad}
				case "revision":
					s.Revision = 0
					if variant == "invalid" {
						s.Revision = math.MaxInt64 + 1
					}
				case "observed":
					s.ObservedAtNS = -1
				case "captured":
					s.CapturedAtNS = -1
				case "size":
					s.Object.Size = -1
					if variant == "invalid" {
						s.Object.Size = math.MaxInt64
					}
				case "encoded-size":
					s.Object.EncodedSize = -1
					if variant == "invalid" {
						s.Object.EncodedSize = math.MaxInt64
					}
				case "encoding":
					s.Object.Encoding = "gzip"
				case "digest":
					s.Object.SHA256 = value
				case "encoded-digest":
					s.Object.EncodedSHA256 = value
				case "key":
					s.Object.Key = "../other-session/object.zst"
				}
			}, validationErrorFor(field)})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := original
			m.Sources = append([]Source(nil), original.Sources...)
			tt.mutate(&m)
			if err := ValidateManifest(m); err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateManifest error = %v, want %q", err, tt.want)
			}
		})
	}
}

func validationErrorFor(field string) string {
	switch field {
	case "observed", "captured", "size", "encoded-size":
		return "invalid source size or timestamp"
	case "encoding", "digest", "encoded-digest":
		return "unsupported encoding or invalid digest"
	case "key":
		return "object key outside session content namespace"
	default:
		return "invalid or duplicate source epoch"
	}
}

func TestManifestValidationValidBoundaries(t *testing.T) {
	_, m, _ := fixture(t)
	m.Sequence = math.MaxInt64
	m.ObservedAtNS = math.MaxInt64
	m.CapturedAtNS = math.MaxInt64
	m.PublishedAtNS = math.MaxInt64
	m.Coverage = Coverage{Discovery: "producer-status", Enrichment: "unknown"}
	s := m.Sources[0]
	s.Revision = math.MaxInt64
	s.Path = ""
	s.Object.Size = math.MaxInt64 - 1
	s.Object.EncodedSize = math.MaxInt64 - 1
	m.Sources = []Source{s, s, s}
	m.Sources[1].EpochID = "another-epoch"
	m.Sources[2].SourceID = "another-source"
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
	m.Sources = nil
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
}

func TestDigestValidationBranches(t *testing.T) {
	for _, digest := range []string{"", "zz", strings.Repeat("a", 62), strings.Repeat("a", 66), strings.Repeat("A", 64)} {
		if validDigest(digest) {
			t.Fatalf("accepted invalid digest %q", digest)
		}
	}
	if !validDigest(strings.Repeat("a", 64)) {
		t.Fatal("valid digest rejected")
	}
}

func TestStrictMetadataValidationBranches(t *testing.T) {
	for name, data := range map[string][]byte{
		"oversize":           []byte(strings.Repeat(" ", MaxMetadata+1)),
		"unknown-field":      []byte(`{"unexpected":true}`),
		"malformed":          []byte(`{`),
		"trailing-value":     []byte(`{} {}`),
		"trailing-malformed": []byte(`{} !`),
	} {
		t.Run(name, func(t *testing.T) {
			var a Archive
			if err := decode(data, &a); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
	data, err := json.Marshal(Archive{Contract: Contract, ArchiveID: "archive"})
	if err != nil {
		t.Fatal(err)
	}
	var a Archive
	if err := decode(append(data, '\n'), &a); err != nil {
		t.Fatal(err)
	}
}

func TestExportRejectsInvalidRootBeforeCreatingBodies(t *testing.T) {
	b, m, _ := fixture(t)
	dest := filepath.Join(t.TempDir(), "unselected")
	if _, err := ExportSession(t.Context(), b, m.Identity, dest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unselected export error = %v, want not found", err)
	}
	if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected export created destination: %v", err)
	}
	publish(t, b, m, "")
	target := t.TempDir()
	dest = filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportSession(t.Context(), b, m.Identity, dest); err == nil {
		t.Fatal("symlink export accepted")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("symlink export changed target")
	}
}

func TestMkdirDurableCreationAndUnsafeParents(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "new", "nested")
	if err := mkdirDurable(nested); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Dir(nested), nested} {
		st, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Fatalf("directory mode = %v, want private directory", st.Mode())
		}
	}
	if err := mkdirDurable(nested); err != nil {
		t.Fatalf("existing directory: %v", err)
	}
	blocked := filepath.Join(parent, "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"existing-file":               blocked,
		"blocked-child":               filepath.Join(blocked, "child"),
		"existing-symlink":            alias,
		"missing-child-under-symlink": filepath.Join(alias, "unsafe-new"),
		"invalid-path":                "nul\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if err := mkdirDurable(dir); err == nil {
				t.Fatal("unsafe directory accepted")
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(parent, "unsafe-new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created directory through symlink: %v", err)
	}
}
