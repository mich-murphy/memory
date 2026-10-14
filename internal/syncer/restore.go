package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

// Restore streams one committed generation to dst and verifies its encoded pieces, decoded
// pieces and complete content hash. Empty id selects the unique current generation. It does
// not need the original source; historical ids remain usable after local deletion/cache loss.
func (s *Syncer) Restore(ctx context.Context, agent, key, id string, dst io.Writer) error {
	if err := s.Recover(ctx); err != nil {
		return err
	}

	if id == "" {
		_, current, err := s.current("file", agent, key)
		if err != nil {
			return err
		}
		if current == nil {
			return errors.New("archive restore: file not found")
		}
		id = current.ID
	}
	found := false
	err := s.db.WalkArchive("file", streamID(agent, key), func(record state.Record) error {
		if !record.Committed || record.ID != id {
			return nil
		}
		found = true
		p, err := decodePublication(record.Body)
		if err != nil {
			return err
		}
		return s.restoreGeneration(ctx, p.File, dst)
	})
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	return errors.New("archive restore: generation not found")
}

func (s *Syncer) restoreGeneration(ctx context.Context, g *raw.Generation, dst io.Writer) error {
	whole := sha256.New()
	var size int64
	for _, piece := range g.Pieces {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.restorePiece(ctx, piece, io.MultiWriter(dst, whole))
		if err != nil {
			return err
		}
		size += n
	}
	if size != g.Size || hex.EncodeToString(whole.Sum(nil)) != g.SHA256 {
		return errors.New("archive restore: whole content checksum or size mismatch")
	}
	return nil
}

func (s *Syncer) restorePiece(ctx context.Context, piece raw.Piece, dst io.Writer) (int64, error) {
	reader, err := s.bucket.OpenReader(ctx, piece.Object)
	if err != nil {
		return 0, err
	}
	defer func() { _ = reader.Close() }() // read-only; io.Copy reports transport errors
	encoded := sha256.New()
	source := io.TeeReader(io.LimitReader(reader, piece.EncodedSize+1), encoded)
	decoder, err := zstd.NewReader(source, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(128<<20))
	if err != nil {
		return 0, err
	}
	defer decoder.Close()
	decoded := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, decoded), io.LimitReader(decoder, piece.End-piece.Start+1))
	if err != nil {
		return n, err
	}
	if n != piece.End-piece.Start || hex.EncodeToString(decoded.Sum(nil)) != piece.SHA256 {
		return n, fmt.Errorf("archive restore %s: decoded checksum or size mismatch", piece.Object)
	}
	if _, err := io.Copy(io.Discard, source); err != nil {
		return n, err
	}
	if hex.EncodeToString(encoded.Sum(nil)) != piece.EncodedSHA256 {
		return n, fmt.Errorf("archive restore %s: encoded checksum mismatch", piece.Object)
	}
	return n, nil
}
