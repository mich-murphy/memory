package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/snapshot"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

func publicationFixture(tb testing.TB, count int) (*Syncer, *memStore, string, map[string][]byte) {
	tb.Helper()
	root := tb.TempDir()
	cfg := &config.Config{Home: root, StateDir: tb.TempDir(), LocalStore: tb.TempDir(), MachineID: "machine"}
	db, err := state.Open(tb.Context(), cfg.StateDir)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := db.Close(); err != nil {
			tb.Error(err)
		}
	})
	bucket := newMemStore()
	sy := New(cfg, bucket, db, adapters.All(root), Options{})
	if err := sy.Recover(tb.Context()); err != nil {
		tb.Fatal(err)
	}
	set := snapshot.Set{Kind: "harness", Agent: "claude", UserBase: "/root", Complete: true}
	bodies := map[string][]byte{}
	for i := range count {
		body := fmt.Appendf(nil, "shared instruction %d", i)
		sha := raw.SHA256(body)
		bodies[plainKey(sha)] = body
		set.Files = append(set.Files, snapshot.File{Scope: "user", Path: fmt.Sprintf("skills/%d", i), SHA256: sha, Size: len(body)})
	}
	body, err := jsonx.SortedCompact(set)
	if err != nil {
		tb.Fatal(err)
	}
	id := raw.SHA256(body)
	bodies[snapshotKeyFor(id)] = body
	bucket.takeOps()
	return sy, bucket, id, bodies
}

func publishSnapshotFanout(tb testing.TB, sy *Syncer, id string, bodies map[string][]byte, sessions int) {
	tb.Helper()
	for i := range sessions {
		p := branchPublication("manifest")
		p.Key = fmt.Sprintf("session%d", i)
		p.Manifest.SessionKey = p.Key
		p.Manifest.Snapshots = []state.SnapshotEntry{{ID: id, TakenAt: 1, Timing: "captured"}}
		deps, err := sy.snapshotDependencies(tb.Context(), id, bodies, p.Agent)
		if err != nil {
			tb.Fatal(err)
		}
		p.Dependencies = deps
		if _, err := sy.publish(tb.Context(), p, bodies); err != nil {
			tb.Fatal(err)
		}
	}
}

// The exact same standalone fixture runs on both revisions: 20 distinct sessions
// referencing one 64-file immutable snapshot. No discovery or raw capture is timed.
func BenchmarkPublicationSnapshotFanout(b *testing.B) {
	for _, rtt := range []time.Duration{0, time.Millisecond} {
		b.Run(fmt.Sprintf("rtt=%s", rtt), func(b *testing.B) {
			var requests int
			for b.Loop() {
				b.StopTimer()
				sy, bucket, id, bodies := publicationFixture(b, 64)
				bucket.latency = rtt
				b.StartTimer()
				publishSnapshotFanout(b, sy, id, bodies, 20)
				b.StopTimer()
				ops, _ := bucket.takeOps()
				requests += len(ops)
				b.StartTimer()
			}
			b.ReportMetric(float64(requests)/float64(b.N), "requests/op")
		})
	}
}

func TestPublicationSharedDependenciesHaveLinearRequests(t *testing.T) {
	for _, sessions := range []int{1, 20} {
		t.Run(fmt.Sprintf("sessions%d", sessions), func(t *testing.T) {
			sy, bucket, id, bodies := publicationFixture(t, 64)
			publishSnapshotFanout(t, sy, id, bodies, sessions)
			ops, _ := bucket.takeOps()
			heads := 0
			for _, op := range ops {
				if strings.HasPrefix(op, "head v3/blobs/") || strings.HasPrefix(op, "head v3/snapshots/") {
					heads++
				}
			}
			if heads != 65 {
				t.Fatalf("dependency HEADs = %d, want 65 distinct dependencies", heads)
			}
			bucket.takeOps()
			for range 20 {
				if _, err := sy.snapshotDependencies(t.Context(), id, nil, "claude"); err != nil {
					t.Fatal(err)
				}
			}
			ops, _ = bucket.takeOps()
			if len(ops) != 0 {
				t.Fatalf("cached snapshot requests = %v, want none", ops)
			}
			bad := maps.Clone(bodies)
			bad[snapshotKeyFor(id)] = []byte("forged")
			if _, err := sy.snapshotDependencies(t.Context(), id, bad, "claude"); err == nil {
				t.Fatal("supplied corrupt set bypassed cache validation")
			}
			if _, err := sy.snapshotDependencies(t.Context(), id, nil, "codex"); err == nil {
				t.Fatal("cached set bypassed agent validation")
			}
		})
	}
}

func TestDependencyCacheValidatesSuppliedBytesAndEntireIdentity(t *testing.T) {
	sy, bucket, _, _ := publicationFixture(t, 0)
	body := []byte("body")
	d := dependency{Object: plainKey(raw.SHA256(body)), SHA256: raw.SHA256(body), Size: 4, Encoding: "plain"}
	if err := sy.ensureDependency(t.Context(), d, map[string][]byte{d.Object: body}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte("evil"), []byte("shorter")} {
		if err := sy.ensureDependency(t.Context(), d, map[string][]byte{d.Object: bad}); err == nil {
			t.Fatal("bad supplied bytes accepted on cache hit")
		}
	}
	for _, mutate := range []func(*dependency){
		func(d *dependency) { d.Size++ }, func(d *dependency) { d.SHA256 = raw.SHA256([]byte("other")) },
		func(d *dependency) { d.Encoding = "zstd" }, func(d *dependency) { d.RawSize++ },
		func(d *dependency) { d.RawSHA256 = raw.SHA256([]byte("other")) },
	} {
		other := d
		mutate(&other)
		if err := sy.ensureDependency(t.Context(), other, nil); err == nil {
			t.Fatalf("conflicting identity accepted: %+v", other)
		}
	}
	encoded := raw.Compress(body)
	compressed := dependency{Object: raw.EncodedKey(raw.SHA256(encoded)), SHA256: raw.SHA256(encoded), Size: int64(len(encoded)), Encoding: "zstd", RawSHA256: raw.SHA256(body), RawSize: 4}
	for _, bad := range []dependency{func() dependency { d := compressed; d.RawSize++; return d }(), func() dependency { d := compressed; d.RawSHA256 = raw.SHA256([]byte("evil")); return d }()} {
		if err := sy.ensureDependency(t.Context(), bad, map[string][]byte{bad.Object: encoded}); err == nil {
			t.Fatal("bad decoded identity accepted")
		}
	}
	if err := sy.ensureDependency(t.Context(), compressed, map[string][]byte{compressed.Object: encoded}); err != nil {
		t.Fatal(err)
	}
	bucket.takeOps()
	if err := sy.ensureDependency(t.Context(), compressed, nil); err != nil {
		t.Fatal(err)
	}
	if ops, _ := bucket.takeOps(); len(ops) != 0 {
		t.Fatalf("confirmed compressed request = %v", ops)
	}
}

type publicationProbeStore struct {
	reads atomic.Int32
	store.Store
	active  atomic.Int32
	peak    atomic.Int32
	entered chan struct{}
	release chan struct{}
	fail    bool
	unknown bool
}

func (p *publicationProbeStore) Head(ctx context.Context, key string) (store.Object, error) {
	active := p.active.Add(1)
	defer p.active.Add(-1)
	for old := p.peak.Load(); active > old; old = p.peak.Load() {
		if p.peak.CompareAndSwap(old, active) {
			break
		}
	}
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return store.Object{}, ctx.Err()
		}
	}
	if p.fail {
		return store.Object{}, fmt.Errorf("injected HEAD %s", key)
	}
	return p.Store.Head(ctx, key)
}

func (p *publicationProbeStore) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	p.reads.Add(1)
	return p.Store.OpenReader(ctx, key)
}

func (p *publicationProbeStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if err := p.Store.Put(ctx, key, body, meta); err != nil {
		return err
	}
	if p.unknown && strings.HasPrefix(key, "v3/blobs/") {
		return errors.New("unknown acknowledgement")
	}
	return nil
}

func TestDependencyWorkersAreGloballyBoundedAndCancelWithoutCommit(t *testing.T) {
	sy, bucket, id, bodies := publicationFixture(t, 40)
	deps, err := sy.snapshotDependencies(t.Context(), id, bodies, "claude")
	if err != nil {
		t.Fatal(err)
	}
	probe := &publicationProbeStore{Store: bucket, entered: make(chan struct{}, 128), release: make(chan struct{})}
	sy.bucket = probe
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		workers.Go(func() { errs <- sy.ensureDependencies(ctx, deps, bodies) })
	}
	// Concurrent publications share both single-flight confirmations and the global limit.
	for range dependencyWorkers {
		select {
		case <-probe.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	workers.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ensureDependencies = %v, want canceled", err)
		}
	}
	if got := probe.peak.Load(); got != dependencyWorkers {
		t.Fatalf("peak = %d, want %d", got, dependencyWorkers)
	}
	if got := probe.active.Load(); got != 0 {
		t.Fatalf("active after return = %d", got)
	}
	// No failures were memoized: a subsequent successful attempt must do work.
	close(probe.release)
	probe.entered = nil
	if err := sy.ensureDependencies(t.Context(), deps, bodies); err != nil {
		t.Fatal(err)
	}
	if ops, _ := bucket.takeOps(); len(ops) == 0 {
		t.Fatal("retry made no requests")
	}
}

func TestDependencyFailureWritesPlanButNeverCommit(t *testing.T) {
	sy, bucket, id, bodies := publicationFixture(t, 40)
	sy.bucket = &publicationProbeStore{Store: bucket, fail: true}
	p := branchPublication("manifest")
	p.Manifest.Snapshots = []state.SnapshotEntry{{ID: id, TakenAt: 1, Timing: "captured"}}
	var err error
	p.Dependencies, err = sy.snapshotDependencies(t.Context(), id, bodies, "claude")
	if err != nil {
		t.Fatal(err)
	}
	_, err = sy.publish(t.Context(), p, bodies)
	if err == nil || !strings.Contains(err.Error(), p.Dependencies[0].Object) {
		t.Fatalf("publish = %v, want first dependency error", err)
	}
	ops, _ := bucket.takeOps()
	plans := 0
	for _, op := range ops {
		if strings.Contains(op, "/commits/") {
			t.Fatalf("failed dependency committed: %s", op)
		}
		if strings.Contains(op, "/plans/") {
			plans++
		}
		if strings.Contains(op, "put v3/blobs/") || strings.Contains(op, "put v3/snapshots/") {
			t.Fatalf("HEAD failure caused write: %s", op)
		}
	}
	if plans != 1 {
		t.Fatalf("plans = %d, want 1", plans)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sy.publish(ctx, p, bodies); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publish = %v", err)
	}
	ops, _ = bucket.takeOps()
	for _, op := range ops {
		if strings.Contains(op, "/commits/") {
			t.Fatal("canceled publication committed")
		}
	}
}

func TestUnknownDependencyAcknowledgementStreamsBeforeCaching(t *testing.T) {
	sy, bucket, _, _ := publicationFixture(t, 0)
	probe := &publicationProbeStore{Store: bucket, unknown: true}
	sy.bucket = probe
	body := []byte("body")
	d := dependency{Object: plainKey(raw.SHA256(body)), SHA256: raw.SHA256(body), Size: 4, Encoding: "plain"}
	if err := sy.ensureDependency(t.Context(), d, map[string][]byte{d.Object: body}); err != nil {
		t.Fatal(err)
	}
	ops, _ := bucket.takeOps()
	if probe.reads.Load() != 1 {
		t.Fatalf("unknown acknowledgement readbacks = %d, want 1 (%v)", probe.reads.Load(), ops)
	}
	if err := sy.ensureDependency(t.Context(), d, nil); err != nil {
		t.Fatal(err)
	}
	if ops, _ := bucket.takeOps(); len(ops) != 0 {
		t.Fatalf("verified dependency requests = %v", ops)
	}
}

func TestSnapshotCacheAndConfirmationAdmissionAreBounded(t *testing.T) {
	sy, _, _, _ := publicationFixture(t, 0)
	body := make([]byte, maxSnapshotCacheSize)
	sy.cacheSnapshot("first", body)
	sy.cacheSnapshot("second", []byte("x"))
	if sy.cachedSnapshot("second") != nil {
		t.Fatal("snapshot cache exceeded limit")
	}
	for i := range maxConfirmedDependencies + 10 {
		sy.confirmDependency(dependency{Object: fmt.Sprintf("object%d", i)})
	}
	if len(sy.dependencies.confirmed) > maxConfirmedDependencies || sy.dependencies.confirmedBytes > maxDependencyCacheSize {
		t.Fatalf("confirmed cache exceeded count/byte bounds: %d, %d", len(sy.dependencies.confirmed), sy.dependencies.confirmedBytes)
	}
	before := len(sy.dependencies.confirmed)
	sy.confirmDependency(dependency{Object: strings.Repeat("x", maxDependencyCacheSize)})
	if len(sy.dependencies.confirmed) != before {
		t.Fatal("oversized identity admitted")
	}
}
