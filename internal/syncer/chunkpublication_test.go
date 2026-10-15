package syncer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

type chunkDiscoveryStore struct {
	store.Store
	mu       sync.Mutex
	heads    map[string]int
	failures map[string]error
	active   atomic.Int32
	peak     atomic.Int32
	entered  chan struct{}
	release  chan struct{}
}

func (c *chunkDiscoveryStore) Head(ctx context.Context, key string) (store.Object, error) {
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for old := c.peak.Load(); active > old; old = c.peak.Load() {
		if c.peak.CompareAndSwap(old, active) {
			break
		}
	}
	c.mu.Lock()
	c.heads[key]++
	failure := c.failures[key]
	c.mu.Unlock()
	if c.entered != nil {
		c.entered <- struct{}{}
	}
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return store.Object{}, ctx.Err()
		}
	}
	if failure != nil {
		return store.Object{}, failure
	}
	return c.Store.Head(ctx, key)
}

// The probe-stage fixture represents 600 unique sealed database pieces with tiny
// compressed bodies, not a 600 MiB compression benchmark. It uses the production
// full dependency identity, run cache, global request slots and private-file owner.
func stagedChunkFixture(t *testing.T, count int, present func(int) bool) (*Syncer, *memStore, *upload, *chunkDiscoveryStore, []string) {
	t.Helper()
	sy, bucket := branchSyncer(t)
	if err := os.MkdirAll(sy.tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := branchPublication("file")
	p.Dependencies = nil
	p.File.Pieces = nil
	p.File.Role = string(adapters.RoleDB)
	u := &upload{publication: p, database: true, stagedPath: filepath.Join(sy.tmpDir, "capture")}
	t.Cleanup(u.cleanup)
	if err := os.WriteFile(u.stagedPath, []byte("private captured snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	var whole []byte
	paths := make([]string, count)
	for i := range count {
		body := fmt.Appendf(nil, "database block %06d", i)
		encoded := raw.Compress(body)
		start := int64(len(whole))
		whole = append(whole, body...)
		part := &upload{bodyPath: filepath.Join(sy.tmpDir, fmt.Sprintf("chunk%d", i)), piece: raw.Piece{Start: start, End: int64(len(whole)), SHA256: raw.SHA256(body)}}
		paths[i] = part.bodyPath
		if err := os.WriteFile(part.bodyPath, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := sealPiece(part); err != nil {
			t.Fatal(err)
		}
		u.parts = append(u.parts, part)
		p.File.Pieces = append(p.File.Pieces, part.piece)
		if present(i) {
			if err := bucket.Put(t.Context(), part.piece.Object, encoded, map[string]string{"sha256": part.piece.SHA256, "encoded_sha256": part.piece.EncodedSHA256}); err != nil {
				t.Fatal(err)
			}
		}
	}
	p.File.Size = int64(len(whole))
	p.File.SHA256 = raw.SHA256(whole)
	if err := p.validate(sy.cfg.MachineID); err != nil {
		t.Fatal(err)
	}
	probe := &chunkDiscoveryStore{Store: bucket, heads: map[string]int{}, failures: map[string]error{}}
	sy.bucket = probe
	bucket.takeOps()
	return sy, bucket, u, probe, paths
}

func waitForChunkWorkers(t *testing.T, probe *chunkDiscoveryStore) {
	t.Helper()
	for range dependencyWorkers {
		select {
		case <-probe.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("chunk workers did not start in parallel")
		}
	}
}

func assertChunkStagingGone(t *testing.T, sy *Syncer) {
	t.Helper()
	entries, err := os.ReadDir(sy.tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries = %v, want empty", entries)
	}
}

func TestDatabaseChunkDiscoveryBatchesSixHundredUniquePieces(t *testing.T) {
	sy, bucket, u, probe, paths := stagedChunkFixture(t, 600, func(i int) bool { return i%2 == 0 })
	probe.entered = make(chan struct{}, 600)
	probe.release = make(chan struct{})
	// Two independent probe batches must share the global limit, not get 16 each.
	first, second := *u, *u
	first.parts, second.parts = u.parts[:300:300], u.parts[300:600:600]
	release := sync.OnceFunc(func() { close(probe.release) })
	t.Cleanup(release)
	done := make(chan error, 2)
	go func() { done <- sy.reuseDatabaseChunks(t.Context(), &first) }()
	go func() { done <- sy.reuseDatabaseChunks(t.Context(), &second) }()
	waitForChunkWorkers(t, probe)
	release()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	u.parts = slices.Concat(first.parts, second.parts)
	if got := probe.peak.Load(); got != dependencyWorkers {
		t.Fatalf("peak discovery = %d, want %d", got, dependencyWorkers)
	}
	if got := probe.active.Load(); got != 0 {
		t.Fatalf("active after return = %d", got)
	}
	if len(probe.heads) != 600 {
		t.Fatalf("distinct HEADs = %d, want 600", len(probe.heads))
	}
	for key, count := range probe.heads {
		if count != 1 {
			t.Fatalf("HEAD %s = %d, want 1", key, count)
		}
	}
	if len(u.parts) != 300 {
		t.Fatalf("retained chunks = %d, want 300 proven misses", len(u.parts))
	}
	for i, path := range paths {
		_, err := os.Stat(path)
		if i%2 == 0 && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("existing chunk staging = %v, want removed", err)
		}
		if i%2 == 1 && err != nil {
			t.Fatalf("missing chunk staging = %v, want retained", err)
		}
	}
	ops, _ := bucket.takeOps()
	for _, op := range ops {
		if !strings.HasPrefix(op, "head ") {
			t.Fatalf("discovery wrote before plan: %s", op)
		}
	}
}

func TestDatabaseChunkDiscoveryReusesConfirmedParentIdentities(t *testing.T) {
	for _, conflicting := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflicting%t", conflicting), func(t *testing.T) {
			sy, _, u, probe, _ := stagedChunkFixture(t, 600, func(int) bool { return true })
			// Production baseline verification, not arbitrary direct cache insertion.
			if err := sy.verifyPublication(t.Context(), u.publication); err != nil {
				t.Fatal(err)
			}
			probe.heads = map[string]int{}
			if conflicting {
				u.parts[0].piece.SHA256 = raw.SHA256([]byte("forged decoded identity"))
			}
			err := sy.reuseDatabaseChunks(t.Context(), u)
			if conflicting {
				if err == nil || !strings.Contains(err.Error(), "conflicting confirmed identity") {
					t.Fatalf("discovery = %v, want identity conflict", err)
				}
				assertChunkStagingGone(t, sy)
			} else if err != nil || len(u.parts) != 0 {
				t.Fatalf("discovery = %v, %d retained, want no new chunks", err, len(u.parts))
			}
			if len(probe.heads) != 0 {
				t.Fatalf("confirmed parent HEADs = %d, want 0", len(probe.heads))
			}
		})
	}
}

func TestDatabaseChunkDiscoveryCancelsAndCleansAllStaging(t *testing.T) {
	sy, _, u, probe, _ := stagedChunkFixture(t, 600, func(int) bool { return false })
	probe.entered = make(chan struct{}, 600)
	probe.release = make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sy.reuseDatabaseChunks(ctx, u) }()
	waitForChunkWorkers(t, probe)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery = %v, want canceled", err)
	}
	if got := probe.active.Load(); got != 0 {
		t.Fatalf("active after cancel = %d", got)
	}
	assertChunkStagingGone(t, sy)
}

func TestDatabaseChunkDiscoveryDoesNotTreatOtherFailuresAsMisses(t *testing.T) {
	sy, bucket, u, probe, _ := stagedChunkFixture(t, 600, func(int) bool { return false })
	first := errors.New("first ordered chunk failure")
	probe.failures[u.parts[10].piece.Object] = first
	probe.failures[u.parts[20].piece.Object] = errors.New("later chunk failure")
	if err := sy.reuseDatabaseChunks(t.Context(), u); !errors.Is(err, first) {
		t.Fatalf("discovery = %v, want first piece-order error", err)
	}
	assertChunkStagingGone(t, sy)
	ops, _ := bucket.takeOps()
	for _, op := range ops {
		if !strings.HasPrefix(op, "head ") {
			t.Fatalf("failed discovery mutated bucket: %s", op)
		}
	}
}

func TestDatabaseChunkDiscoveryCleansOtherPathsWhenRemovalFails(t *testing.T) {
	sy, _, u, _, paths := stagedChunkFixture(t, 3, func(int) bool { return true })
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths[0], 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(paths[0], "blocker")
	if err := os.WriteFile(blocker, []byte("cannot remove nonempty directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sy.reuseDatabaseChunks(t.Context(), u); err == nil {
		t.Fatal("staging removal failure was ignored")
	}
	for _, path := range paths[1:] {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("other staging survived removal failure: %s, %v", path, err)
		}
	}
	if u.parts[0].bodyPath != paths[0] {
		t.Fatal("failed removal path lost ownership")
	}
	if u.stagedPath != "" {
		t.Fatal("captured staging not cleaned on removal failure")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := discardDatabaseStaging(u); err != nil {
		t.Fatal(err)
	}
	assertChunkStagingGone(t, sy)
}

func TestDatabaseChunkBatchingPreservesPlanOrderAndUploadsDuplicatesOnce(t *testing.T) {
	sy, bucket := branchSyncer(t)
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sf := adapters.SessionFile{Agent: "claude", Root: sy.cfg.Home, Path: filepath.Join(sy.cfg.Home, "database.sqlite"), Key: "database.sqlite", Role: adapters.RoleDB}
	if err := os.WriteFile(sf.Path, bytes.Repeat([]byte("x"), 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	u, err := sy.prepare(t.Context(), sf)
	if err != nil || u == nil {
		t.Fatalf("prepare = %v, %v", u, err)
	}
	defer u.cleanup()
	bucket.takeOps()
	if err := sy.upload(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	bucket.mu.Lock()
	ops := append([]string(nil), bucket.ops...)
	bucket.mu.Unlock()
	plan, put, commit := -1, -1, -1
	puts, heads := 0, 0
	for i, op := range ops {
		switch {
		case strings.HasPrefix(op, "head v3/blobs/zstd/"):
			heads++
		case strings.Contains(op, "put ") && strings.Contains(op, "/plans/"):
			plan = i
		case strings.HasPrefix(op, "put v3/blobs/zstd/"):
			put = i
			puts++
		case strings.Contains(op, "put ") && strings.Contains(op, "/commits/"):
			commit = i
		}
	}
	if plan < 0 || put <= plan || commit <= put {
		t.Fatalf("publication order plan=%d, blob=%d, commit=%d: %v", plan, put, commit, ops)
	}
	if puts != 1 || heads != 1 || len(u.parts) != 1 || len(u.publication.File.Pieces) != 8 {
		t.Fatalf("duplicate chunks: PUTs=%d HEADs=%d staged=%d pieces=%d", puts, heads, len(u.parts), len(u.publication.File.Pieces))
	}
	if u.outcome.Status != captureCommitted {
		t.Fatalf("outcome = %s, want committed", u.outcome.Status)
	}
}

func TestDatabaseChunkSealingFailureCleansCapturedAndCompressedPaths(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled%t", canceled), func(t *testing.T) {
			sy, bucket := branchSyncer(t)
			if err := os.MkdirAll(sy.tmpDir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(sy.tmpDir, "capture")
			// A valid first chunk followed by a truncated chunk exercises partial sealing.
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20), 0o600); err != nil {
				t.Fatal(err)
			}
			u := &upload{stagedPath: path, database: true, file: state.File{Size: 8 << 20}, publication: branchPublication("file")}
			defer u.cleanup()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			if err := sy.chunkDatabase(ctx, u); err == nil {
				t.Fatal("invalid/canceled sealing succeeded")
			}
			assertChunkStagingGone(t, sy)
			if ops, _ := bucket.takeOps(); len(ops) != 0 {
				t.Fatalf("failed sealing accessed bucket: %v", ops)
			}
		})
	}
}

type chunkPlanFailureStore struct{ store.Store }

func (c *chunkPlanFailureStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if strings.Contains(key, "/plans/") {
		return errors.New("injected plan failure")
	}
	return c.Store.Put(ctx, key, body, meta)
}

func TestDatabaseChunkMissesDoNotUploadWhenPlanFails(t *testing.T) {
	sy, bucket := branchSyncer(t)
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sf := adapters.SessionFile{Agent: "claude", Root: sy.cfg.Home, Path: filepath.Join(sy.cfg.Home, "database.sqlite"), Key: "database.sqlite", Role: adapters.RoleDB}
	if err := os.WriteFile(sf.Path, bytes.Repeat([]byte("x"), 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	u, err := sy.prepare(t.Context(), sf)
	if err != nil || u == nil {
		t.Fatalf("prepare = %v, %v", u, err)
	}
	defer u.cleanup()
	sy.bucket = &chunkPlanFailureStore{Store: bucket}
	bucket.takeOps()
	if err := sy.upload(t.Context(), u); err == nil || !strings.Contains(err.Error(), "injected plan failure") {
		t.Fatalf("upload = %v, want plan failure", err)
	}
	ops, _ := bucket.takeOps()
	for _, op := range ops {
		if strings.HasPrefix(op, "put ") {
			t.Fatalf("failed plan allowed upload: %s", op)
		}
	}
	u.cleanup()
	assertChunkStagingGone(t, sy)
}
