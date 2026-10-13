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
	sha      string
	size     int64
	lines    int64
	appended bool
	resume   *state.Resume
}

type digest struct {
	sha   hash.Hash
	fast  *xxhash.Digest
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
		if withSHA {
			_, _ = d.sha.Write(p)
		}
		d.lines += int64(bytes.Count(p, []byte("\n")))
		d.n += int64(n)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read: %w", err)
		}
		if err != nil || n == 0 {
			return nil
		}
	}
	return nil
}

func (d *digest) result(appended bool) hashed {
	h := hashed{sha: hex.EncodeToString(d.sha.Sum(nil)), size: d.n, lines: d.lines, appended: appended}
	if m, ok := d.sha.(encoding.BinaryMarshaler); ok {
		if mid, err := m.MarshalBinary(); err == nil {
			h.resume = &state.Resume{Size: d.n, Fast: d.fast.Sum64(), SHA: mid}
		}
	}
	return h
}

// hashContent reads src's first size bytes once and says whether they start with the bytes
// last uploaded (prev). With saved left by hashing prev, those bytes are checked by xxhash and
// only the appended ones go through SHA-256, so a sync after one turn of a long session reads
// the session at memory speed rather than SHA-256 speed.
func hashContent(src io.ReaderAt, size int64, prev *state.Upload, saved *state.Resume) (hashed, error) {
	old := int64(-1)
	if prev != nil && prev.Size != nil && *prev.Size <= size {
		old = *prev.Size
	}
	buf := make([]byte, min(max(size, 1), chunkSize))
	if saved != nil && saved.Size == old {
		d := newDigest(buf)
		if err := d.read(src, old, false); err != nil {
			return hashed{}, err
		}
		if u, ok := d.sha.(encoding.BinaryUnmarshaler); ok && d.n == old && d.fast.Sum64() == saved.Fast &&
			u.UnmarshalBinary(saved.SHA) == nil {
			if err := d.read(src, size, true); err != nil {
				return hashed{}, err
			}
			return d.result(true), nil
		}
	}
	d := newDigest(buf)
	appended := false
	if old >= 0 {
		if err := d.read(src, old, true); err != nil {
			return hashed{}, err
		}
		appended = d.n == old && hex.EncodeToString(d.sha.Sum(nil)) == prev.SHA256
	}
	if err := d.read(src, size, true); err != nil {
		return hashed{}, err
	}
	return d.result(appended), nil
}
