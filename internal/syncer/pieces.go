package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/store"
)

const (
	databaseChunkThreshold int64 = 8 << 20
	databaseChunkSize      int64 = 1 << 20
)

// preparePieces seals every descriptor before the plan is published. Database chunks
// stream from the same private snapshot used for the whole-file digest. Only compressed
// staging paths and descriptors accumulate; neither the database nor all chunks live in RAM.
func (s *Syncer) preparePieces(ctx context.Context, u *upload) error {
	if u.reuse {
		return s.verifyPublication(ctx, u.publication)
	}
	if u.database {
		if err := s.chunkDatabase(ctx, u); err != nil {
			return err
		}
	} else {
		if err := u.compress(u); err != nil {
			return err
		}
		if err := sealPiece(u); err != nil {
			return err
		}
		u.publication.File.Pieces = append(u.publication.File.Pieces, u.piece)
		// The owner cleans its ordinary piece; this borrowed upload only owns the PUT.
		u.parts = []*upload{{piece: u.piece, body: u.body, bodyPath: u.bodyPath}}
	}
	// Verify inherited pieces before publishing a plan. Newly staged database pieces
	// are checked for reuse in chunkDatabase, including repeats within this generation.
	if !u.database {
		deps := u.publication.dependencies()
		return s.ensureDependencies(ctx, deps[:len(deps)-1], nil)
	}

	return nil
}

func (s *Syncer) chunkDatabase(ctx context.Context, u *upload) error {
	if err := s.sealDatabaseChunks(ctx, u); err != nil {
		return errors.Join(err, discardDatabaseStaging(u))
	}
	return s.reuseDatabaseChunks(ctx, u)
}

// Seal from the one captured snapshot before issuing remote requests. RAM holds
// only descriptors and private path names; duplicate bodies are removed promptly.
func (s *Syncer) sealDatabaseChunks(ctx context.Context, u *upload) error {
	size := u.file.Size
	length := max(size, 1)
	if size >= databaseChunkThreshold {
		length = databaseChunkSize
	}
	seen := map[string]dependency{}
	for start := int64(0); start < max(size, 1); start += length {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+length, size)
		part := &upload{piece: raw.Piece{Start: start, End: end}}
		// Register before fallible work; ownership persists until successful removal.
		u.parts = append(u.parts, part)
		digest := sha256.New()
		var err error
		part.bodyPath, err = s.compressToTemp(u.stagedPath, start, end-start, digest)
		if err != nil {
			return err
		}
		part.piece.SHA256 = hex.EncodeToString(digest.Sum(nil))
		if err := sealPiece(part); err != nil {
			return err
		}
		u.publication.File.Pieces = append(u.publication.File.Pieces, part.piece)
		d := pieceDependency(part.piece)
		if previous, ok := seen[d.Object]; ok {
			if previous != d {
				return fmt.Errorf("archive dependency %s: conflicting database chunk identity", d.Object)
			}
			if err := os.Remove(part.bodyPath); err != nil {
				return err
			}
			part.bodyPath = ""
			u.parts = u.parts[:len(u.parts)-1]
		} else {
			seen[d.Object] = d
		}
	}
	return nil
}

// Probe all sealed unique chunks before retaining only proven misses. NotFound
// applies to that chunk alone, never the rest of the batch. Worker errors are
// selected in piece order; successful confirmations still populate the run cache.
func (s *Syncer) reuseDatabaseChunks(ctx context.Context, u *upload) (err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, discardDatabaseStaging(u))
		}
	}()
	errs := s.probeDatabaseChunks(ctx, u.parts)
	for _, err := range errs {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	missing := make([]*upload, 0, len(u.parts))
	for i, part := range u.parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if errors.Is(errs[i], store.ErrNotFound) {
			missing = append(missing, part)
			continue
		}
		if err := os.Remove(part.bodyPath); err != nil {
			return err
		}
		part.bodyPath = ""
	}
	u.parts = missing
	return nil
}

// Scheduling is separate from staging ownership: workers only report the exact
// result for each unique sealed part, and all join before any path is removed.
func (s *Syncer) probeDatabaseChunks(ctx context.Context, parts []*upload) []error {
	errs := make([]error, len(parts))
	var next atomic.Uint64
	var workers sync.WaitGroup
	for range min(dependencyWorkers, len(parts)) {
		workers.Go(func() {
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= len(parts) {
					return
				}
				errs[i] = s.ensureDependency(ctx, pieceDependency(parts[i].piece), nil)
			}
		})
	}
	workers.Wait()
	return errs
}

// Failed preparation owns no publishable data. Attempt every removal even when
// one fails, reporting failures and retaining those paths for the caller's cleanup.
func discardDatabaseStaging(u *upload) error {
	var errs []error
	for _, part := range u.parts {
		if err := discardDatabaseStaging(part); err != nil {
			errs = append(errs, err)
		}
	}
	for _, path := range []*string{&u.bodyPath, &u.stagedPath} {
		if *path == "" {
			continue
		}
		if err := os.Remove(*path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		} else {
			*path = ""
		}
	}
	return errors.Join(errs...)
}
