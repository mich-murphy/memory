package syncer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

const (
	dependencyWorkers        = 16
	maxConfirmedDependencies = 65536
	maxSnapshotCacheSize     = 8 << 20
	maxDependencyCacheSize   = 16 << 20
)

type dependencyFlight struct {
	identity dependency
	done     chan struct{}
}

// All entries are immutable confirmations for this lifecycle, not arbitrary local
// projections. Admission limits bound memory; saturation only costs extra requests.
type dependencyCache struct {
	mu             sync.Mutex
	confirmed      map[string]dependency
	flights        map[string]dependencyFlight
	slots          chan struct{}
	snapshots      map[string][]byte
	snapshotBytes  int
	confirmedBytes int
}

func (c *dependencyCache) init() {
	if c.confirmed == nil {
		c.confirmed = map[string]dependency{}
		c.flights = map[string]dependencyFlight{}
		c.slots = make(chan struct{}, dependencyWorkers)
		c.snapshots = map[string][]byte{}
	}
}

func (s *Syncer) confirmDependency(d dependency) {
	c := &s.dependencies
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	c.remember(d)
}

// remember runs under mu. Count and byte caps bound both many small identities
// and unusually large object keys. Saturation never changes validation semantics.
func (c *dependencyCache) remember(d dependency) {
	size := len(d.Object) + len(d.SHA256) + len(d.RawSHA256) + len(d.Encoding) + 256
	if _, ok := c.confirmed[d.Object]; !ok && len(c.confirmed) < maxConfirmedDependencies && c.confirmedBytes+size <= maxDependencyCacheSize {
		c.confirmed[d.Object] = d
		c.confirmedBytes += size
	}
}

func (s *Syncer) confirmRecord(key string, body []byte) {
	if strings.Contains(key, "/commits/") {
		s.confirmDependency(dependency{Object: key, SHA256: raw.SHA256(body), Size: int64(len(body)), Encoding: "plain"})
	}
}

// A completed recovery proves the baseline: either matching CLEAN local/remote
// checkpoints, or exhaustive remote validation and unique-chain replay. Before
// that boundary SQLite is not evidence. File pieces still get a first changed-file
// check each run, preserving the missing-history boundary on rewrites.
// External mutation after this boundary is outside the same contract as warm stat-unchanged capture (not an archive audit).
func (s *Syncer) confirmBaseline(p *publication, r *state.Record) {
	if !s.recovered || r == nil || !r.Committed || p == nil || p.validate(s.cfg.MachineID) != nil || raw.SHA256(r.Body) != r.ID || r.Key != s.publicationKey(p, true, r.ID) {
		return
	}
	s.confirmRecord(r.Key, r.Body)
}

func validateDependencyBody(d dependency, body []byte) error {
	if int64(len(body)) != d.Size || raw.SHA256(body) != d.SHA256 {
		return fmt.Errorf("archive dependency %s: supplied checksum or size mismatch", d.Object)
	}
	if d.Encoding == "zstd" {
		decoder, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(128<<20))
		if err != nil {
			return err
		}
		defer decoder.Close()
		decoded := sha256.New()
		size, err := io.Copy(decoded, io.LimitReader(decoder, d.RawSize+1))
		if err != nil {
			return err
		}
		if size != d.RawSize || hex.EncodeToString(decoded.Sum(nil)) != d.RawSHA256 {
			return fmt.Errorf("archive dependency %s: supplied raw checksum or size mismatch", d.Object)
		}
	}
	return nil
}

func (s *Syncer) ensureDependency(ctx context.Context, d dependency, bodies map[string][]byte) error {
	body, supplied := bodies[d.Object]
	// Supplied bytes must agree even when a prior publication confirmed the object.
	if supplied {
		if err := validateDependencyBody(d, body); err != nil {
			return err
		}
	}
	c := &s.dependencies
	done, err := c.acquire(ctx, d)
	if err != nil || done == nil {
		return err
	}
	err = s.checkDependency(ctx, d, body, supplied)
	c.mu.Lock()
	if err == nil {
		c.remember(d)
	}
	delete(c.flights, d.Object)
	close(done)
	c.mu.Unlock()
	return err
}

// acquire separates cache coordination from store I/O. A non-nil channel is an
// exclusive verification lease; waiters retry rather than inheriting a failure.
func (c *dependencyCache) acquire(ctx context.Context, d dependency) (chan struct{}, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.init()
		if held, ok := c.confirmed[d.Object]; ok {
			c.mu.Unlock()
			if held != d {
				return nil, fmt.Errorf("archive dependency %s: conflicting confirmed identity", d.Object)
			}
			return nil, nil
		}
		if flight, ok := c.flights[d.Object]; ok {
			c.mu.Unlock()
			if flight.identity != d {
				return nil, fmt.Errorf("archive dependency %s: conflicting pending identity", d.Object)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
				continue
			}
		}
		done := make(chan struct{})
		c.flights[d.Object] = dependencyFlight{identity: d, done: done}
		c.mu.Unlock()
		return done, nil
	}
}

func (s *Syncer) dependencySlot(ctx context.Context) (func(), error) {
	c := &s.dependencies
	c.mu.Lock()
	c.init()
	slots := c.slots
	c.mu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// putPiece shares the global dependency limit with inherited-piece HEAD checks.
func (s *Syncer) putPiece(ctx context.Context, u *upload) error {
	release, err := s.dependencySlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	meta := map[string]string{"sha256": u.piece.SHA256, "encoded_sha256": u.piece.EncodedSHA256}
	if u.bodyPath != "" {
		err = s.bucket.PutFile(ctx, u.piece.Object, u.bodyPath, meta)
	} else {
		err = s.bucket.Put(ctx, u.piece.Object, u.body, meta)
	}
	if err != nil {
		if verify := verifyDependency(ctx, s.bucket, pieceDependency(u.piece)); verify != nil {
			return errors.Join(err, verify)
		}
	}
	s.confirmDependency(pieceDependency(u.piece))
	return nil
}

func (s *Syncer) checkDependency(ctx context.Context, d dependency, body []byte, supplied bool) error {
	release, err := s.dependencySlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	object, err := s.bucket.Head(ctx, d.Object)
	if err == nil {
		return validateDependencyMetadata(d, object)
	}
	if !supplied || !errors.Is(err, store.ErrNotFound) {
		return err
	}
	meta := map[string]string{"sha256": d.SHA256}
	if d.Encoding == "zstd" {
		meta = map[string]string{"sha256": d.RawSHA256, "encoded_sha256": d.SHA256}
	}
	if err := s.bucket.Put(ctx, d.Object, body, meta); err != nil {
		if verify := verifyDependency(ctx, s.bucket, d); verify != nil {
			return errors.Join(err, verify)
		}
	}
	// Store.Put success confirms the supplied bytes and metadata. Unknown answers
	// above require streamed readback, not merely our just-supplied HEAD metadata.
	return nil
}

// Workers finish before returning, including on cancellation. Errors are selected
// in descriptor order, independent of completion order. We don't cancel independent
// checks on the first failure: successful checks remain reusable, but no commit is
// possible. Parent cancellation stops scheduling and propagates to every request.
func (s *Syncer) ensureDependencies(ctx context.Context, deps []dependency, bodies map[string][]byte) error {
	// Snapshot sets are publication descriptors too: don't upload a new set before
	// its bodies are durable. Keep the existing crash-after-blob boundary fail-closed.
	if len(bodies) == 0 {
		return s.checkDependencies(ctx, deps, bodies)
	}
	blobs := make([]dependency, 0, len(deps))
	var sets []dependency
	for _, d := range deps {
		if strings.HasPrefix(d.Object, "v3/snapshots/sets/") {
			sets = append(sets, d)
		} else {
			blobs = append(blobs, d)
		}
	}
	if err := s.checkDependencies(ctx, blobs, bodies); err != nil {
		return err
	}
	return s.checkDependencies(ctx, sets, bodies)
}

func (s *Syncer) checkDependencies(ctx context.Context, deps []dependency, bodies map[string][]byte) error {
	var next atomic.Uint64
	errs := make([]error, len(deps))
	var workers sync.WaitGroup
	for range min(dependencyWorkers, len(deps)) {
		workers.Go(func() {
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= len(deps) {
					return
				}
				errs[i] = s.ensureDependency(ctx, deps[i], bodies)
			}
		})
	}
	workers.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *Syncer) cachedSnapshot(key string) []byte {
	c := &s.dependencies
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshots[key]
}

func (s *Syncer) cacheSnapshot(key string, body []byte) {
	c := &s.dependencies
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	if _, ok := c.snapshots[key]; !ok && c.snapshotBytes+len(body) <= maxSnapshotCacheSize {
		c.snapshots[key] = bytes.Clone(body)
		c.snapshotBytes += len(body)
	}
}
