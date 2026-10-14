package syncer

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

func contractArchive(t *testing.T) (*Syncer, *config.Config, *state.State, *memStore) {
	t.Helper()
	cfg := &config.Config{Home: t.TempDir(), StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	bucket := newMemStore()
	return New(cfg, bucket, db, reviewAdapters(cfg.Home), Options{}), cfg, db, bucket
}

func TestAliasAuthorityIsIndependentOfOrderingAndPreservesAcknowledgedHistory(t *testing.T) {
	for _, tc := range []struct {
		name, a, b, want, failure string
		compressed                bool
	}{
		{name: "equal", a: "same", b: "same", want: "same"},
		{name: "prefix", a: "A", b: "AB-longer", want: "AB-longer"},
		{name: "equalPlainCompressed", a: "same", b: "same", want: "same", compressed: true},
		{name: "emptyPrefixCompressed", a: "", b: "longer", want: "longer", compressed: true},
		{name: "divergent", a: "alpha", b: "bravo", failure: "divergent"},
		{name: "longerDivergent", a: "abc", b: "abd-longer", failure: "divergent"},
		{name: "missing", a: "same", failure: "no such file"},
		{name: "invalidZstd", a: "same", b: "not a zstd frame", compressed: true, failure: ""},
		{name: "roleMismatch", a: "same", b: "same", failure: "conflicting logical identities"},
		{name: "sessionMismatch", a: "same", b: "same", failure: "conflicting logical identities"},
		{name: "rootMismatch", a: "same", b: "same", failure: "conflicting logical identities"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sy, cfg, db, bucket := contractArchive(t)
			a := adapters.SessionFile{Agent: "claude", Root: cfg.Home, Path: filepath.Join(cfg.Home, "a"), Key: "alias", Role: adapters.RoleAux}
			b := a
			b.Path = filepath.Join(cfg.Home, "b")
			b.Zstd = tc.compressed
			if err := os.WriteFile(a.Path, []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			sum, err := sy.Run(t.Context(), []adapters.SessionFile{a})
			if err != nil || sum.Errors != 0 {
				t.Fatalf("initial Run = %+v, %v", sum, err)
			}
			_, previous, err := sy.current("file", a.Agent, a.Key)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(a.Path, []byte(tc.a), 0o600); err != nil {
				t.Fatal(err)
			}
			body := []byte(tc.b)
			if b.Zstd && tc.name != "invalidZstd" {
				body = raw.Compress(body)
			}
			if tc.name != "missing" {
				if err := os.WriteFile(b.Path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch tc.name {
			case "roleMismatch":
				b.Role = adapters.RoleUnknown
			case "sessionMismatch":
				b.SessionKey = "other"
			case "rootMismatch":
				b.Root = t.TempDir()
			}
			// Mtime cannot elect a winner: the shorter or divergent source is deliberately newer.
			future := time.Now().Add(time.Hour)
			if err := os.Chtimes(a.Path, future, future); err != nil {
				t.Fatal(err)
			}
			failed := tc.failure != "" || tc.name == "invalidZstd"
			for _, files := range [][]adapters.SessionFile{{b, a, b}, {a, b}} {
				sy = New(cfg, bucket, db, reviewAdapters(cfg.Home), Options{})
				sum, err = sy.Run(t.Context(), files)
				if err != nil {
					t.Fatal(err)
				}
				if failed {
					if sum.Errors != 1 || sum.Uploaded != 0 || tc.failure != "" && !strings.Contains(strings.Join(sum.ErrorMessages, " "), tc.failure) {
						t.Fatalf("conflicting/failed alias Run = %+v, want deferred explicit error", sum)
					}
				} else if sum.Errors != 0 {
					t.Fatalf("alias Run = %+v", sum)
				}
				want := tc.want
				if failed {
					want = "before"
				}
				var restored bytes.Buffer
				if err := sy.Restore(t.Context(), a.Agent, a.Key, "", &restored); err != nil {
					t.Fatal(err)
				}
				if restored.String() != want {
					t.Fatalf("current Restore = %q, want %q", restored.String(), want)
				}
				restored.Reset()
				if err := sy.Restore(t.Context(), a.Agent, a.Key, previous.ID, &restored); err != nil || restored.String() != "before" {
					t.Fatalf("acknowledged Restore = %q, %v, want before", restored.String(), err)
				}
				entries, err := os.ReadDir(sy.tmpDir)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Fatalf("alias staging leaked %d files", len(entries))
				}
				if !failed {
					current, _, err := sy.current("file", a.Agent, a.Key)
					if err != nil || len(current.File.Sources) != 2 || current.File.Sources[0].Path != a.Path || current.File.Sources[1].Path != b.Path {
						t.Fatalf("source provenance = %+v, %v", current, err)
					}
				}
			}
		})
	}
}

func TestAliasComparisonCancellationDoesNotPublishCandidates(t *testing.T) {
	sy, cfg, _, bucket := contractArchive(t)
	a := adapters.SessionFile{Agent: "claude", Root: cfg.Home, Path: filepath.Join(cfg.Home, "a"), Key: "alias", Role: adapters.RoleAux}
	b := a
	b.Path = filepath.Join(cfg.Home, "b")
	for _, path := range []string{a.Path, b.Path} {
		if err := os.WriteFile(path, []byte("same"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sy.groupAliases(ctx, []adapters.SessionFile{a, b}); !errors.Is(err, context.Canceled) {
		t.Fatalf("groupAliases = %v, want cancellation", err)
	}
	if len(bucket.objects) != 0 {
		t.Fatal("canceled alias comparison mutated destination")
	}
}

func TestConfigAliasesCompareScrubbedBytesAndRejectMalformedSources(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "differentSecretAliases"
		if malformed {
			name = "malformedProtectedAlias"
		}
		t.Run(name, func(t *testing.T) {
			sy, cfg, _, _ := contractArchive(t)
			var files []adapters.SessionFile
			for _, dir := range []string{"a", "b"} {
				path := filepath.Join(cfg.Home, dir, "models.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				body := `{"providers":{"p":{"apiKey":"synthetic-secret-` + dir + `"}}}`
				if malformed && dir == "b" {
					body = `{"providers":`
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				files = append(files, adapters.SessionFile{Agent: "claude", Root: cfg.Home, Path: path, Key: "configAlias", Role: adapters.RoleAux})
			}
			got, err := sy.groupAliases(t.Context(), files)
			if err != nil {
				t.Fatal(err)
			}
			if malformed {
				if len(got) != 0 || sy.Summary.Errors != 1 || !strings.Contains(strings.Join(sy.Summary.ErrorMessages, " "), "scrub") {
					t.Fatalf("malformed aliases = %+v, summary = %+v", got, sy.Summary)
				}
			} else if len(got) != 1 || got[0].Path != files[0].Path || sy.Summary.Errors != 0 {
				t.Fatalf("equal scrubbed aliases = %+v, summary = %+v", got, sy.Summary)
			}
			entries, err := os.ReadDir(sy.tmpDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("staged config aliases remaining = %v, %v", entries, err)
			}
		})
	}
}
