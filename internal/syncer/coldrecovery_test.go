package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

type overlapStore struct {
	store.Store
	latency        time.Duration
	heads          atomic.Int64
	inflight, peak atomic.Int64
}

func (o *overlapStore) Head(ctx context.Context, key string) (store.Object, error) {
	n := o.inflight.Add(1)
	defer o.inflight.Add(-1)
	for old := o.peak.Load(); n > old && !o.peak.CompareAndSwap(old, n); old = o.peak.Load() {
	}
	o.heads.Add(1)
	time.Sleep(o.latency)
	return o.Store.Head(ctx, key)
}

func coldFixture(t *testing.T, files int) (*config.Config, *memStore, *state.State) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	sfs := make([]adapters.SessionFile, files)
	for i := range sfs {
		key := fmt.Sprintf("file%03d", i)
		path := filepath.Join(root, key)
		if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
		sfs[i] = adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: key, Role: adapters.RoleAux}
	}
	bucket := newMemStore()
	sum, err := New(cfg, bucket, db, adapters.All(root), Options{}).Run(t.Context(), sfs)
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	if err := db.SetCheckpoint("dirty", false); err != nil {
		t.Fatal(err)
	}
	return cfg, bucket, db
}

func TestColdRecoveryVerifiesCachedRecordsConcurrently(t *testing.T) {
	const files = 300
	cfg, bucket, db := coldFixture(t, files)
	probe := &overlapStore{Store: bucket, latency: 2 * time.Millisecond}
	if err := New(cfg, probe, db, adapters.All(cfg.Home), Options{}).Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := probe.heads.Load(); got != 3*files {
		t.Errorf("cold recovery HEADs = %d, want %d", got, 3*files)
	}
	if got := probe.peak.Load(); got < 2 || got > dependencyWorkers {
		t.Errorf("cold recovery requests in flight = %d, want 2..%d", got, dependencyWorkers)
	}
}

func TestColdRecoveryRejectsAChangedCachedRecord(t *testing.T) {
	cfg, bucket, db := coldFixture(t, 2*coldBatchSize)
	var commits []string
	for key := range bucket.objects {
		if strings.Contains(key, "/commits/") {
			commits = append(commits, key)
		}
	}
	if len(commits) == 0 {
		t.Fatal("no commits")
	}
	changed := commits[len(commits)/2]
	obj := bucket.objects[changed]
	bucket.objects[changed] = memObject{body: obj.body, meta: map[string]string{"sha256": strings.Repeat("0", 64)}}
	err := New(cfg, bucket, db, adapters.All(cfg.Home), Options{}).Recover(t.Context())
	if err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("Recover = %v, want checksum changed", err)
	}
	if _, clean, err := db.Checkpoint(); err != nil || clean {
		t.Fatalf("checkpoint clean = %v, %v, want dirty", clean, err)
	}
}
