package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/state"
)

func TestWarmIdleRequestsDoNotGrowWithFilesOrHistory(t *testing.T) {
	for _, fileCount := range []int{1, 30} {
		t.Run(fmt.Sprintf("files%d", fileCount), func(t *testing.T) {
			root := t.TempDir()
			cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
			bucket := newMemStore()
			db, err := state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			files := make([]adapters.SessionFile, fileCount)
			for i := range files {
				key := fmt.Sprintf("file%d", i)
				path := filepath.Join(root, key)
				if err := os.WriteFile(path, []byte("A"), 0o600); err != nil {
					t.Fatal(err)
				}
				files[i] = adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: key, Role: adapters.RoleAux}
			}
			run := func() {
				t.Helper()
				sy := New(cfg, bucket, db, adapters.All(root), Options{})
				sum, err := sy.Run(t.Context(), files)
				if err != nil || sum.Errors != 0 {
					t.Fatalf("Run = %+v, %v", sum, err)
				}
			}
			run()
			// A long history and many idle files must not change no-op request volume.
			for i := range 20 {
				if err := os.WriteFile(files[0].Path, []byte(fmt.Sprintf("generation%d", i)), 0o600); err != nil {
					t.Fatal(err)
				}
				run()
			}
			bucket.takeOps()
			run()
			ops, _ := bucket.takeOps()
			if len(ops) > 8 {
				t.Fatalf("warm idle requests = %d (%v), want <=8 independent of files/history", len(ops), ops)
			}
			for _, op := range ops {
				if strings.Contains(op, "/records/") {
					t.Fatalf("idle archive metadata request: %s", op)
				}
			}
		})
	}
}

// Changed streams enumerate immutable record names/sizes but do not HEAD every previously
// verified plan/commit. This catches a regression hidden by a zero-body-download oracle.
func TestChangedStreamDoesNotHeadItsEntireHistory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	bucket := newMemStore()
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sf := adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: "file", Role: adapters.RoleAux}
	for i := range 30 {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("version%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		sy := New(cfg, bucket, db, adapters.All(root), Options{})
		sum, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
		if err != nil || sum.Errors != 0 {
			t.Fatalf("Run = %+v, %v", sum, err)
		}
	}
	bucket.takeOps()
	if err := os.WriteFile(path, []byte("last"), 0o600); err != nil {
		t.Fatal(err)
	}
	sy := New(cfg, bucket, db, adapters.All(root), Options{})
	sum, err := sy.Run(context.Background(), []adapters.SessionFile{sf})
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	ops, _ := bucket.takeOps()
	heads := 0
	for _, op := range ops {
		if strings.HasPrefix(op, "head ") && strings.Contains(op, "/records/") {
			heads++
		}
	}
	if heads > 1 {
		t.Fatalf("historical record HEAD requests = %d, want <=1", heads)
	}
}
