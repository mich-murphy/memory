package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

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
		for _, piece := range u.publication.File.Pieces[:len(u.publication.File.Pieces)-1] {
			if err := s.ensureDependency(ctx, pieceDependency(piece), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Syncer) chunkDatabase(ctx context.Context, u *upload) error {
	size := u.file.Size
	length := max(size, 1)
	if size >= databaseChunkThreshold {
		length = databaseChunkSize
	}
	seen := map[string]bool{}
	for start := int64(0); start < max(size, 1); start += length {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+length, size)
		part := &upload{piece: raw.Piece{Start: start, End: end}}
		// Register before any fallible work so cancellation and partial failures clean up.
		u.parts = append(u.parts, part)
		digest := sha256.New()
		path, err := s.compressToTemp(u.stagedPath, start, end-start, digest)
		if err != nil {
			return err
		}
		part.bodyPath = path
		part.piece.SHA256 = hex.EncodeToString(digest.Sum(nil))
		if err := sealPiece(part); err != nil {
			return err
		}
		u.publication.File.Pieces = append(u.publication.File.Pieces, part.piece)
		missing := false
		if !seen[part.piece.Object] {
			err := s.ensureDependency(ctx, pieceDependency(part.piece), nil)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			missing = errors.Is(err, store.ErrNotFound)
		}
		seen[part.piece.Object] = true
		if missing {
			continue
		}

		// Existing immutable chunks and repeats need no PUT or staging body.
		if err := os.Remove(path); err != nil {
			return err
		}
		part.bodyPath = ""
		u.parts = u.parts[:len(u.parts)-1]
	}
	return nil
}
