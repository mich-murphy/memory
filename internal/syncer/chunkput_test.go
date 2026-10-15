package syncer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

type chunkPutStore struct {
	store.Store
	mu       sync.Mutex
	plans    map[string]bool
	objects  map[string]bool
	puts     map[string]int
	failures map[string]error
	active   atomic.Int32
	peak     atomic.Int32
	reads    atomic.Int32
	entered  chan struct{}
	release  chan struct{}
	unknown  bool
	corrupt  bool
}

func (c *chunkPutStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if !strings.HasPrefix(key, "v3/blobs/zstd/") {
		if err := c.Store.Put(ctx, key, body, meta); err != nil {
			return err
		}
		if strings.Contains(key, "/plans/") {
			p, err := decodePublication(body)
			if err != nil {
				return err
			}
			c.mu.Lock()
			c.plans[key] = true
			for _, d := range p.dependencies() {
				c.objects[d.Object] = true
			}
			c.mu.Unlock()
		}
		return nil
	}
	c.mu.Lock()
	c.puts[key]++
	planned := c.objects[key]
	failure := c.failures[key]
	c.mu.Unlock()
	if !planned {
		return errors.New("piece PUT before its nonce-prefixed durable plan")
	}
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for old := c.peak.Load(); active > old; old = c.peak.Load() {
		if c.peak.CompareAndSwap(old, active) {
			break
		}
	}
	if c.entered != nil {
		c.entered <- struct{}{}
	}
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if failure != nil {
		return failure
	}
	if c.corrupt {
		body = bytes.Clone(body)
		body[len(body)-1] ^= 0xff
	}
	if err := c.Store.Put(ctx, key, body, meta); err != nil {
		return err
	}
	if c.unknown {
		return errors.New("unknown piece acknowledgement")
	}
	return nil
}

func (c *chunkPutStore) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return c.Put(ctx, key, body, meta)
}

func (c *chunkPutStore) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.HasPrefix(key, "v3/blobs/") {
		c.reads.Add(1)
	}
	return c.Store.OpenReader(ctx, key)
}

func chunkPutFixture(t *testing.T, count int) (*Syncer, *memStore, *upload, *chunkPutStore) {
	t.Helper()
	sy, bucket, u, _, _ := stagedChunkFixture(t, count, func(int) bool { return false })
	// A per-test nonce in the logical stream makes an unrelated plan insufficient.
	u.publication.Key = "nonce-" + raw.SHA256([]byte(t.Name()))
	u.publication.File.Identity.Key = u.publication.Key
	sy.bucket = bucket
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	probe := &chunkPutStore{Store: bucket, plans: map[string]bool{}, objects: map[string]bool{}, puts: map[string]int{}, failures: map[string]error{}}
	sy.bucket = probe
	bucket.takeOps()
	return sy, bucket, u, probe
}

func waitForPiecePuts(t *testing.T, probe *chunkPutStore) {
	t.Helper()
	for range dependencyWorkers {
		select {
		case <-probe.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("piece PUT workers did not start in parallel")
		}
	}
}

func clonePreparedCapture(u *upload, key string) *upload {
	v := *u
	p := *u.publication
	g := *p.File
	p.File = &g
	p.Key, g.Identity.Key = key, key
	v.publication = &p
	v.parts = make([]*upload, len(u.parts))
	for i, part := range u.parts {
		cloned := *part
		cloned.sent = 0
		v.parts[i] = &cloned
	}
	v.sent = 0
	return &v
}

func assertNoPieceCommit(t *testing.T, bucket *memStore) {
	t.Helper()
	ops, _ := bucket.takeOps()
	for _, op := range ops {
		if strings.Contains(op, "put ") && strings.Contains(op, "/commits/") {
			t.Fatalf("failed piece publication committed: %s", op)
		}
	}
}

func TestPiecePutsAreGloballyBoundedAndWaitForNoncePlans(t *testing.T) {
	sy, _, u, probe := chunkPutFixture(t, 600)
	// Independent files race for the same new content. Each writes its own plan,
	// but every full immutable identity is uploaded only once across both batches.
	v := clonePreparedCapture(u, "other-"+u.publication.Key)
	probe.entered, probe.release = make(chan struct{}, 1200), make(chan struct{})
	release := sync.OnceFunc(func() { close(probe.release) })
	t.Cleanup(release)
	done := make(chan error, 2)
	go func() { done <- sy.publishCapture(t.Context(), u) }()
	go func() { done <- sy.publishCapture(t.Context(), v) }()
	waitForPiecePuts(t, probe)
	release()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := probe.peak.Load(); got != dependencyWorkers {
		t.Fatalf("peak PUTs = %d, want %d", got, dependencyWorkers)
	}
	if got := probe.active.Load(); got != 0 {
		t.Fatalf("active after return = %d", got)
	}
	if len(probe.plans) != 2 || len(probe.puts) != 600 {
		t.Fatalf("plans=%d distinct PUTs=%d, want2/600", len(probe.plans), len(probe.puts))
	}
	var want int64
	for _, part := range u.parts {
		want += part.piece.EncodedSize
		if probe.puts[part.piece.Object] != 1 {
			t.Fatalf("piece PUT count = %d, want1", probe.puts[part.piece.Object])
		}
	}
	if u.sent+v.sent != want {
		t.Fatalf("confirmed write bytes = %d, want %d once", u.sent+v.sent, want)
	}
	w := clonePreparedCapture(u, "third-"+u.publication.Key)
	if err := sy.publishCapture(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	if w.sent != 0 {
		t.Fatalf("cache-hit write bytes = %d, want0", w.sent)
	}
	for _, count := range probe.puts {
		if count != 1 {
			t.Fatalf("lifecycle duplicate PUTs = %d", count)
		}
	}
}

func TestPiecePutCancellationJoinsBeforeReturningWithoutCommit(t *testing.T) {
	sy, bucket, u, probe := chunkPutFixture(t, 600)
	probe.entered, probe.release = make(chan struct{}, 600), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sy.publishCapture(ctx, u) }()
	waitForPiecePuts(t, probe)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("publishCapture = %v, want canceled", err)
	}
	if got := probe.active.Load(); got != 0 {
		t.Fatalf("active after cancel = %d", got)
	}
	if u.sent != 0 {
		t.Fatalf("canceled write bytes = %d, want0", u.sent)
	}
	assertNoPieceCommit(t, bucket)
}

func TestPiecePutFailurePreservesColdParentAndRetriesOnlyFailedWrites(t *testing.T) {
	sy, bucket, u, probe := chunkPutFixture(t, 40)
	parent := clonePreparedCapture(u, u.publication.Key)
	parent.parts = parent.parts[:1]
	parent.publication.File.Pieces = parent.publication.File.Pieces[:1]
	parent.publication.File.Size = parent.parts[0].piece.End
	parent.publication.File.SHA256 = parent.parts[0].piece.SHA256
	if err := sy.publishCapture(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	_, id, err := canonicalPublication(parent.publication)
	if err != nil {
		t.Fatal(err)
	}
	u.publication.Sequence, u.publication.File.Sequence = 2, 2
	u.publication.Parent, u.publication.File.Parent = id, id
	u.previous = parent.publication.File
	first := errors.New("first descriptor PUT failure")
	probe.failures[u.parts[10].piece.Object] = first
	probe.failures[u.parts[20].piece.Object] = errors.New("later descriptor PUT failure")
	bucket.takeOps()
	if err := sy.publishCapture(t.Context(), u); !errors.Is(err, first) {
		t.Fatalf("publishCapture = %v, want first descriptor error", err)
	}
	if probe.active.Load() != 0 {
		t.Fatal("failed batch returned with active PUTs")
	}
	assertNoPieceCommit(t, bucket)
	db, err := state.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	cold := New(sy.cfg, bucket, db, sy.adapters, Options{})
	if err := cold.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, record, err := cold.current("file", u.publication.Agent, u.publication.Key)
	if err != nil || current == nil || record.ID != id {
		t.Fatalf("cold current = %+v, %+v, %v, want durable parent", current, record, err)
	}
	var restored bytes.Buffer
	if err := cold.Restore(t.Context(), u.publication.Agent, u.publication.Key, "", &restored); err != nil || raw.SHA256(restored.Bytes()) != parent.publication.File.SHA256 {
		t.Fatalf("cold parent Restore = %v, %q, want acknowledged parent bytes", err, restored.Bytes())
	}
	// Missing/error outcomes weren't cached; successful writes were. Retrying within
	// the original run uploads exactly the two failed parts and no confirmed part.
	probe.failures = map[string]error{}
	probe.puts = map[string]int{}
	if err := sy.putPieces(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if len(probe.puts) != 2 {
		t.Fatalf("retry PUTs = %d, want2", len(probe.puts))
	}
	want := u.parts[10].piece.EncodedSize + u.parts[20].piece.EncodedSize
	if u.sent != want {
		t.Fatalf("retry write bytes = %d, want%d", u.sent, want)
	}
}

func TestPiecePutCacheHitStillRejectsBodyAndIdentityMismatch(t *testing.T) {
	sy, _, u, probe := chunkPutFixture(t, 1)
	if err := sy.publishCapture(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*upload){
		func(p *upload) { p.piece.EncodedSize++ },
		func(p *upload) { p.piece.EncodedSHA256 = raw.SHA256([]byte("other")) },
		func(p *upload) { p.piece.SHA256 = raw.SHA256([]byte("wrong decoded")) },
		func(p *upload) { p.piece.End++ },
	} {
		bad := *u.parts[0]
		mutate(&bad)
		if err := sy.putPiece(t.Context(), &bad); err == nil {
			t.Fatal("cache hit accepted conflicting piece identity")
		}
	}
	if err := os.WriteFile(u.parts[0].bodyPath, []byte("corrupt staged body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sy.putPiece(t.Context(), u.parts[0]); err == nil {
		t.Fatal("cache hit skipped private staged body validation")
	}
	if probe.puts[u.parts[0].piece.Object] != 1 {
		t.Fatal("invalid piece reached PUT")
	}
}

func TestPiecePutUnknownAcknowledgementStreamsBeforeReleasingWaiters(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt%t", corrupt), func(t *testing.T) {
			sy, _, u, probe := chunkPutFixture(t, 1)
			probe.unknown, probe.corrupt = true, corrupt
			err := sy.publishCapture(t.Context(), u)
			if (err != nil) != corrupt {
				t.Fatalf("publishCapture = %v, corrupt%t", err, corrupt)
			}
			if probe.reads.Load() != 1 {
				t.Fatalf("ambiguous readbacks = %d, want1", probe.reads.Load())
			}
			probe.unknown, probe.corrupt = false, false
			if err := sy.putPieces(t.Context(), u); err != nil {
				t.Fatal(err)
			}
			want := 1
			if corrupt {
				want = 2
			}
			if probe.puts[u.parts[0].piece.Object] != want {
				t.Fatalf("PUTs after retry = %d, want%d", probe.puts[u.parts[0].piece.Object], want)
			}
			if !corrupt && u.sent != 0 {
				t.Fatal("reconciled cache hit counted another write")
			}
		})
	}
}

func TestPiecePutAndHeadChecksShareOneCancelableIdentityLease(t *testing.T) {
	sy, _, u, probe := chunkPutFixture(t, 1)
	body, id, err := canonicalPublication(u.publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := sy.putRecord(t.Context(), sy.publicationKey(u.publication, false, id), body); err != nil {
		t.Fatal(err)
	}
	probe.entered, probe.release = make(chan struct{}, 2), make(chan struct{})
	release := sync.OnceFunc(func() { close(probe.release) })
	t.Cleanup(release)
	done := make(chan error, 1)
	go func() { done <- sy.putPieces(t.Context(), u) }()
	select {
	case <-probe.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("piece PUT did not start")
	}
	d := pieceDependency(u.parts[0].piece)
	sy.dependencies.mu.Lock()
	flight, exists := sy.dependencies.flights[d.Object]
	sy.dependencies.mu.Unlock()
	if !exists || flight.identity != d {
		t.Fatal("PUT did not acquire the full identity lease before request slot")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := sy.ensureDependency(ctx, d, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled HEAD waiter = %v", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := sy.ensureDependency(t.Context(), d, nil); err != nil {
		t.Fatal(err)
	}
	if len(sy.dependencies.flights) != 0 {
		t.Fatal("completed PUT retained lease")
	}
	// Canceled retries cannot report a prior successful attempt's bytes.
	if err := sy.putPieces(ctx, u); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled retry = %v", err)
	}
	if u.sent != 0 {
		t.Fatalf("canceled retry write bytes = %d, want0", u.sent)
	}
}

func TestPiecePutSummaryCountsWritesOnceAcrossValidatedLifecycles(t *testing.T) {
	sy, bucket := branchSyncer(t)
	body := []byte("identical captured bytes\n")
	makeFile := func(key string) adapters.SessionFile {
		t.Helper()
		path := filepath.Join(sy.cfg.Home, key)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return adapters.SessionFile{Agent: "claude", Root: sy.cfg.Home, Path: path, Key: key, Role: adapters.RoleAux}
	}
	sum, err := sy.Run(t.Context(), []adapters.SessionFile{makeFile("first"), makeFile("second")})
	want := int64(len(raw.Compress(body)))
	if err != nil || sum.Errors != 0 || sum.Uploaded != 2 || sum.BytesUploaded != want {
		t.Fatalf("Run = %+v, %v, want two captures/one %d-byte write", sum, err, want)
	}
	// Only a proven cold/recovered baseline can seed another lifecycle's cache.
	if err := sy.db.SetCheckpoint(sy.runToken, false); err != nil {
		t.Fatal(err)
	}
	next := New(sy.cfg, bucket, sy.db, sy.adapters, Options{})
	bucket.takeOps()
	sum, err = next.Run(t.Context(), []adapters.SessionFile{makeFile("third")})
	if err != nil || sum.Errors != 0 || sum.Uploaded != 1 || sum.BytesUploaded != 0 {
		t.Fatalf("recovered Run = %+v, %v, want capture with no new blob write", sum, err)
	}
	ops, _ := bucket.takeOps()
	for _, op := range ops {
		if strings.HasPrefix(op, "put v3/blobs/zstd/") {
			t.Fatalf("recovered confirmed content written again: %s", op)
		}
	}
}
