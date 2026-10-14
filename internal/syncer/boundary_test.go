package syncer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

var errBoundary = errors.New("publication boundary interrupted")

type interruptedArchive struct {
	store.Store
	stage   string
	stopped bool
	land    bool
}

func (f *interruptedArchive) matches(key string, body []byte) bool {
	switch f.stage {
	case "plan":
		return strings.Contains(key, "/records/file/") && strings.Contains(key, "/plans/")
	case "blob":
		return strings.HasPrefix(key, "v3/blobs/zstd/")
	case "commit":
		return strings.Contains(key, "/records/file/") && strings.Contains(key, "/commits/")
	case "clean":
		return strings.HasSuffix(key, "/checkpoint.json") && bytes.Contains(body, []byte(`"complete":true`))
	default:
		return false
	}
}

func (f *interruptedArchive) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if f.stopped {
		return errBoundary
	}
	if !f.matches(key, body) {
		return f.Store.Put(ctx, key, body, meta)
	}
	if f.land {
		if err := f.Store.Put(ctx, key, body, meta); err != nil {
			return err
		}
	}
	f.stopped = true
	return errBoundary
}

func (f *interruptedArchive) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return f.Put(ctx, key, body, meta)
}

func (f *interruptedArchive) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if f.stopped {
		return nil, errBoundary
	}
	return f.Store.OpenReader(ctx, key)
}

func (f *interruptedArchive) Head(ctx context.Context, key string) (store.Object, error) {
	if f.stopped {
		return store.Object{}, errBoundary
	}
	return f.Store.Head(ctx, key)
}

func TestEveryPublicationBoundaryPreservesAcknowledgedGenerations(t *testing.T) {
	for _, stage := range []string{"plan", "blob", "commit", "clean"} {
		for _, land := range []bool{false, true} {
			t.Run(stage+map[bool]string{false: "Before", true: "AfterLostACK"}[land], func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "file")
				cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
				bucket := newMemStore()
				sf := adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: "file", Role: adapters.RoleAux}
				write := func(value string) {
					t.Helper()
					if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
						t.Fatal(err)
					}
					old := time.Now().Add(-time.Hour)
					if err := os.Chtimes(path, old, old); err != nil {
						t.Fatal(err)
					}
				}
				run := func(st store.Store) (*Summary, error) {
					t.Helper()
					db, err := state.Open(t.Context(), cfg.StateDir)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := db.Close(); err != nil {
							t.Error(err)
						}
					}()
					sy := New(cfg, st, db, adapters.All(root), Options{})
					return sy.Run(t.Context(), []adapters.SessionFile{sf})
				}
				write("A")
				summary, err := run(bucket)
				if err != nil || summary.Errors != 0 {
					t.Fatalf("first Run = %+v, %v", summary, err)
				}
				write("AB")
				fault := &interruptedArchive{Store: bucket, stage: stage, land: land}
				summary, err = run(fault)
				if err == nil && summary.Errors == 0 {
					t.Fatal("injected boundary did not interrupt run")
				}
				if !fault.stopped {
					t.Fatal("selected publication boundary not exercised")
				}
				// Neither source nor cache may be used to recover A or the landed AB dependency.
				if err := os.RemoveAll(cfg.StateDir); err != nil {
					t.Fatal(err)
				}
				write("C")
				summary, err = run(bucket)
				if err != nil || summary.Errors != 0 {
					t.Fatalf("recovery Run = %+v, %v", summary, err)
				}
				db, err := state.Open(t.Context(), cfg.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}()
				sy := New(cfg, bucket, db, adapters.All(root), Options{})
				if err := sy.Recover(t.Context()); err != nil {
					t.Fatal(err)
				}
				records, err := db.ArchiveRecords("file", streamID(sf.Agent, sf.Key))
				if err != nil {
					t.Fatal(err)
				}
				contents := map[string]bool{}
				for i := range records {
					r := &records[i]
					if !r.Committed {
						continue
					}
					var out bytes.Buffer
					if err := sy.Restore(t.Context(), sf.Agent, sf.Key, r.ID, &out); err != nil {
						t.Fatal(err)
					}
					contents[out.String()] = true
				}
				if !contents["A"] || !contents["C"] {
					t.Fatalf("committed contents = %v, want A and C", contents)
				}
				// Once AB's blob landed after a durable plan, exact recovery must retain AB even when
				// its PUT/commit/checkpoint response was lost. A plan alone has no captured bytes.
				landedAB := stage == "blob" && land || stage == "commit" || stage == "clean"
				if landedAB && !contents["AB"] {
					t.Fatalf("acknowledged/landed AB disappeared: %v", contents)
				}
			})
		}
	}
}
