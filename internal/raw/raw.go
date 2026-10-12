// Package raw compresses raw objects and reads a raw file back from its pieces.
//
// A raw file is stored as a base object plus the append segments uploaded since it was last
// folded (see docs/storage-layout.md). Each object is zstd and holds bytes [start, end) of the
// file; the file is their concatenation in start order.
package raw

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Level is the zstd level objects are written at, about zstd's level 7. It compresses
// transcripts within a few percent of level 10 at a fraction of the CPU.
const Level = zstd.SpeedBetterCompression

// maxDecoded bounds what one object may expand to, so a corrupt object can't exhaust memory.
const maxDecoded = 16 << 30

var (
	encoderOnce sync.Once
	encoder     *zstd.Encoder
	decoderOnce sync.Once
	decoder     *zstd.Decoder
)

func sharedEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		// Options are constants, so this can't fail. Single segment makes every frame record
		// its content size, as Python's zstandard does; EncodeAll only sees files below the
		// syncer's 64 MB streaming threshold, well within zstd's default 128 MB window limit.
		encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(Level), zstd.WithLowerEncoderMem(true),
			zstd.WithSingleSegment(true))
	})
	return encoder
}

func sharedDecoder() *zstd.Decoder {
	decoderOnce.Do(func() {
		decoder, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxDecoded), zstd.WithDecoderLowmem(true))
	})
	return decoder
}

// Compress returns data as one zstd frame that records its content size, so any zstd reader,
// including Python's zstandard.decompress, can decode it.
func Compress(data []byte) []byte {
	return sharedEncoder().EncodeAll(data, make([]byte, 0, len(data)/4+64))
}

// streamMin is the smallest length CompressStream streams. zstd's frame header has no room for
// a content size under 256 bytes unless the frame is a single segment, which only EncodeAll
// writes, so shorter inputs go through Compress.
const streamMin = 256

// CompressStream compresses the next length bytes of src into dst and returns their sha256.
// The frame records length as its content size.
func CompressStream(src io.Reader, dst io.Writer, length int64) (string, error) {
	if length < streamMin {
		return compressSmall(src, dst, length)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(Level), zstd.WithLowerEncoderMem(true),
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

// Decompress decodes every zstd frame in body.
func Decompress(body []byte) ([]byte, error) {
	out, err := sharedDecoder().DecodeAll(body, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd: %w", err)
	}
	return out, nil
}

// SHA256 is the hex sha256 of data.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SegmentKey names the object holding bytes [start, end) appended to rawKey. The offsets are
// zero-padded so the segments list in order.
func SegmentKey(rawKey string, start, end int64) string {
	return fmt.Sprintf("%s.d/%015d-%015d.zst", rawKey, start, end)
}

// BlobKey names the object holding earlier bytes with this sha256, stored once.
func BlobKey(sha string) string {
	return "blobs/" + sha + ".zst"
}

// Piece is one object of a raw file: bytes [Start, End) with their sha256.
type Piece struct {
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
	SHA256 string `json:"sha256"`
	Object string `json:"object"`
}

// Join rebuilds a file from pieces as manifests list them, checking each piece's length and
// sha256. get returns an object's body.
func Join(get func(key string) ([]byte, error), pieces []Piece) ([]byte, error) {
	sorted := slices.Clone(pieces)
	slices.SortFunc(sorted, func(a, b Piece) int { return cmp.Compare(a.Start, b.Start) })
	var out []byte
	for _, p := range sorted {
		if p.Start != int64(len(out)) {
			return nil, fmt.Errorf("gap or overlap at byte %d: next piece starts at %d", len(out), p.Start)
		}
		body, err := get(p.Object)
		if err != nil {
			return nil, err
		}
		data, err := Decompress(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Object, err)
		}
		if int64(len(data)) != p.End-p.Start || SHA256(data) != p.SHA256 {
			return nil, fmt.Errorf("%s does not match its sha256 or length", p.Object)
		}
		out = append(out, data...)
	}
	return out, nil
}

// Reader is the part of a store ReadKey needs.
type Reader interface {
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
}

// ReadKey rebuilds a file from the bucket alone, without a manifest: the base object, then each
// segment that starts where the bytes so far end. Segments left behind by a fold that could not
// delete them start before that point and are skipped.
func ReadKey(ctx context.Context, store Reader, rawKey string) ([]byte, error) {
	body, err := store.Get(ctx, rawKey)
	if err != nil {
		return nil, err
	}
	out, err := Decompress(body)
	if err != nil {
		return nil, err
	}
	keys, err := store.List(ctx, rawKey+".d/")
	if err != nil {
		return nil, err
	}
	slices.Sort(keys)
	for _, key := range keys {
		start, _, err := segmentRange(key)
		if err != nil {
			return nil, err
		}
		if start != int64(len(out)) {
			continue
		}
		body, err := store.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		data, err := Decompress(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		out = append(out, data...)
	}
	return out, nil
}

var errSegmentName = errors.New("not a segment key")

func segmentRange(key string) (start, end int64, err error) {
	name := key[strings.LastIndexByte(key, '/')+1:]
	a, b, ok := strings.Cut(strings.TrimSuffix(name, ".zst"), "-")
	if !ok {
		return 0, 0, fmt.Errorf("%s: %w", key, errSegmentName)
	}
	if start, err = strconv.ParseInt(a, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("%s: %w", key, errSegmentName)
	}
	if end, err = strconv.ParseInt(b, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("%s: %w", key, errSegmentName)
	}
	return start, end, nil
}
