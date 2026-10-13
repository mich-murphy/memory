// Package raw compresses raw objects and reads a raw file back from its pieces: a base object
// plus the append segments uploaded since it was last folded (docs/storage-layout.md).
package raw

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// level is about zstd's level 7. It compresses transcripts within a few percent of level 10 at
// a fraction of the CPU.
const level = zstd.SpeedBetterCompression

const maxDecodedSize = 16 << 30

var (
	encoderOnce sync.Once
	encoder     *zstd.Encoder
	decoderOnce sync.Once
	decoder     *zstd.Decoder
)

func sharedEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		// Options are constants, so this can't fail. A single segment records the content
		// size; EncodeAll only sees files below the syncer's 64 MB streaming threshold, well
		// within zstd's default 128 MB window limit.
		encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithLowerEncoderMem(true),
			zstd.WithSingleSegment(true))
	})
	return encoder
}

func sharedDecoder() *zstd.Decoder {
	decoderOnce.Do(func() {
		decoder, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxDecodedSize), zstd.WithDecoderLowmem(true))
	})
	return decoder
}

// Compress records the content size in the frame, so any zstd reader, including Python's
// zstandard.decompress, can decode it in one call.
func Compress(data []byte) []byte {
	return sharedEncoder().EncodeAll(data, make([]byte, 0, len(data)/4+64))
}

// zstd's frame header has no room for a content size under 256 bytes unless the frame is a
// single segment, which only EncodeAll writes, so shorter inputs go through Compress.
const streamMin = 256

func CompressStream(src io.Reader, dst io.Writer, length int64) (string, error) {
	if length < streamMin {
		return compressSmall(src, dst, length)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithLowerEncoderMem(true),
		zstd.WithEncoderConcurrency(1))
	if err != nil {
		return "", fmt.Errorf("zstd writer: %w", err)
	}
	enc.ResetContentSize(dst, length)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(h, enc), io.LimitReader(src, length))
	if err != nil {
		_ = enc.Close()
		return "", fmt.Errorf("compress: %w", err)
	}
	if n != length {
		_ = enc.Close()
		return "", fmt.Errorf("compress: read %d bytes, expected %d (file shrank)", n, length)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("compress: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func compressSmall(src io.Reader, dst io.Writer, length int64) (string, error) {
	data := make([]byte, length)
	if _, err := io.ReadFull(src, data); err != nil {
		return "", fmt.Errorf("compress: read %d bytes: %w (file shrank)", length, err)
	}
	if _, err := dst.Write(Compress(data)); err != nil {
		return "", fmt.Errorf("compress: %w", err)
	}
	return SHA256(data), nil
}

func Decompress(body []byte) ([]byte, error) {
	out, err := sharedDecoder().DecodeAll(body, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd: %w", err)
	}
	return out, nil
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SegmentKey zero-pads the offsets so the segments list in order.
func SegmentKey(rawKey string, start, end int64) string {
	return fmt.Sprintf("%s.d/%015d-%015d.zst", rawKey, start, end)
}

func BlobKey(sha string) string {
	return "blobs/" + sha + ".zst"
}

type Piece struct {
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
	SHA256 string `json:"sha256"`
	Object string `json:"object"`
}
