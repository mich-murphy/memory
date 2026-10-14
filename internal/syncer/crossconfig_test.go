package syncer

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

func TestCrossAgentConfigCannotBypassStreamingScrub(t *testing.T) {
	old := largeFileSize
	largeFileSize = 1 << 20
	t.Cleanup(func() { largeFileSize = old })
	for _, compressed := range []bool{false, true} {
		name := "plain"
		if compressed {
			name = "zstd"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			// The logical and final names are unknown; only an intermediate cross-agent
			// identity selects Pi's protected schema while Claude collects this skill.
			body := []byte(`{"providers":{"p":{"apiKey":"synthetic-cross-agent-secret"}}}` + strings.Repeat(" ", 2<<20))
			stored := body
			if compressed {
				stored = raw.Compress(body)
			}
			target := filepath.Join(home, "payload")
			if err := os.WriteFile(target, stored, 0o600); err != nil {
				t.Fatal(err)
			}
			intermediate := filepath.Join(home, "models.json")
			path := filepath.Join(home, "skill")
			for link, to := range map[string]string{intermediate: target, path: intermediate} {
				if err := os.Symlink(to, link); err != nil {
					t.Fatal(err)
				}
			}
			cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
			db, err := state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			sy := New(cfg, newMemStore(), db, adapters.All(home), Options{})
			sf := adapters.SessionFile{Agent: "claude", Root: home, Path: path, Key: "skill", Role: adapters.RoleAux, Zstd: compressed}
			sum, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
			if err != nil || sum.Errors != 0 {
				t.Fatalf("Run = %+v, %v", sum, err)
			}
			var restored bytes.Buffer
			if err := sy.Restore(t.Context(), "claude", "skill", "", &restored); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(restored.Bytes(), []byte("synthetic-cross-agent-secret")) || !bytes.Contains(restored.Bytes(), []byte("[redacted]")) {
				t.Fatal("cross-agent config did not redact protected value")
			}
			// Alias comparison must also use every intermediate identity, not raw bytes.
			capture, err := sy.captureAlias(t.Context(), sf)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Remove(capture.path) }() // disposable comparison staging
			if capture.sha != raw.SHA256(restored.Bytes()) {
				t.Fatalf("alias SHA = %s, want scrubbed %s", capture.sha, raw.SHA256(restored.Bytes()))
			}
		})
	}
}

func TestLargeCrossAgentConfigRejectsRawStagingBypass(t *testing.T) {
	const length int64 = 65 << 20 // Above the normal raw streaming threshold, not just a test threshold.
	home := t.TempDir()
	path := filepath.Join(home, "models.json")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"providers":{"p":{"apiKey":"synthetic-large-secret"}}}`); err != nil {
		t.Fatal(errors.Join(err, f.Close()))
	}
	_, err = io.CopyN(f, &repeatingInput{pattern: []byte(" ")}, length)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write fixture = %v, close = %v", err, closeErr)
	}
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	bucket := newMemStore()
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	sum, err := sy.Run(t.Context(), []adapters.SessionFile{{Agent: "claude", Root: home, Path: path, Key: "models.json", Role: adapters.RoleAux}})
	if err != nil || sum.Errors != 1 || !strings.Contains(strings.Join(sum.ErrorMessages, " "), "bounded parse limit") {
		t.Fatalf("Run = %+v, %v, want bounded config error", sum, err)
	}
	for key := range bucket.objects {
		if strings.HasPrefix(key, "v3/blobs/") || strings.Contains(key, "/records/file/") {
			t.Fatalf("oversized protected config published %s", key)
		}
	}
}
