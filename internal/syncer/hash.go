package syncer

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/cespare/xxhash/v2"

	"github.com/mich-murphy/memory/internal/state"
)

type hashed struct {
	sha           string
	size          int64
	lines         int64
	appended      bool
	prefixNewline bool
	resume        *state.Resume
	// tail is the xxhash of the bytes after the last upload, which a segment holds.
	tail uint64
}

type digest struct {
	sha   hash.Hash
	fast  *xxhash.Digest
	tail  *xxhash.Digest
	lines int64
	n     int64
	buf   []byte
}

func newDigest(buf []byte) *digest {
	return &digest{sha: sha256.New(), fast: xxhash.New(), buf: buf}
}

// read hashes src from d.n up to end, or to its end if that comes first.
func (d *digest) read(src io.ReaderAt, end int64, withSHA bool) error {
	for d.n < end {
		n, err := src.ReadAt(d.buf[:min(int64(len(d.buf)), end-d.n)], d.n)
		p := d.buf[:n]
		_, _ = d.fast.Write(p) // hash writes never fail
		if d.tail != nil {
			_, _ = d.tail.Write(p)
		}
		if withSHA {
			_, _ = d.sha.Write(p)
		}
		d.lines += int64(bytes.Count(p, []byte("\n")))
		d.n += int64(n)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read: %w", err)
		}
		if errors.Is(err, io.EOF) || n == 0 {
			return nil
		}
	}
	return nil
}

func (d *digest) result(appended bool) hashed {
	h := hashed{sha: hex.EncodeToString(d.sha.Sum(nil)), size: d.n, lines: d.lines, appended: appended}
	if d.tail != nil {
		h.tail = d.tail.Sum64()
	}
	if m, ok := d.sha.(encoding.BinaryMarshaler); ok {
		if mid, err := m.MarshalBinary(); err == nil {
			h.resume = &state.Resume{Size: d.n, Fast: d.fast.Sum64(), SHA: mid}
		}
	}
	return h
}

// hashContent hashes src's first size bytes and checks whether the previous upload is an
// unchanged prefix using SHA-256. It hashes the full prefix and suffix, not just appended
// bytes; the saved Resume argument is currently ignored. It also rereads the prefix's last
// byte to determine whether an append starts at a line boundary.
func hashContent(src io.ReaderAt, size int64, prev *state.Upload, _ *state.Resume) (hashed, error) {
	old := int64(-1)
	if prev != nil && prev.Size <= size {
		old = prev.Size
	}
	buf := make([]byte, min(max(size, 1), chunkSize))

	d := newDigest(buf)
	appended := false
	prefixNewline := false
	if old >= 0 {
		if err := d.read(src, old, true); err != nil {
			return hashed{}, err
		}
		appended = d.n == old && hex.EncodeToString(d.sha.Sum(nil)) == prev.SHA256
		if old > 0 {
			var last [1]byte
			if _, err := src.ReadAt(last[:], old-1); err != nil {
				return hashed{}, err
			}
			prefixNewline = last[0] == '\n'
		}
		d.tail = xxhash.New()
	}
	if err := d.read(src, size, true); err != nil {
		return hashed{}, err
	}
	h := d.result(appended)
	h.prefixNewline = prefixNewline
	return h, nil
}

// Compression checks the cryptographic digest of the bytes prepared for this piece.
func (u *upload) setPieceSHA(h hash.Hash) error {
	u.piece.SHA256 = hex.EncodeToString(h.Sum(nil))
	expected := u.expectedSHA
	if expected == "" && u.piece.Start == 0 {
		expected = u.file.SHA256
	}
	if expected != "" && u.piece.SHA256 != expected {
		return errRewritten
	}
	return nil
}

var errRewritten = errors.New("changed while being read; the next sync uploads it")
