package publicationr2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

// hangReader counts reads instead of waiting: a zero-length buffer would make
// verifyDigest spin, which the stall limit turns into an immediate error.
type hangReader struct {
	r        io.Reader
	reads    int
	firstLen int
	limit    int
}

func (h *hangReader) Read(p []byte) (int, error) {
	h.reads++
	if h.reads == 1 {
		h.firstLen = len(p)
	}
	if h.reads > h.limit {
		return 0, errors.New("stalled")
	}
	return h.r.Read(p)
}

func TestHangVerifyDigestReadsInBoundedChunks(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789"), 10_000)
	sum := sha256.Sum256(data)
	r := &hangReader{r: bytes.NewReader(data), limit: 50}
	if err := verifyDigest(t.Context(), r, int64(len(data)), hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("verifyDigest: %v", err)
	}
	if r.firstLen != 32*1024 || r.reads != 5 {
		t.Fatalf("first buffer %d, reads %d; want 32768 and 5", r.firstLen, r.reads)
	}
}

func TestHangSafeErrorClassifiesNilPlainAndCancelled(t *testing.T) {
	if err := safeError(t.Context(), nil); err != nil {
		t.Fatalf("nil: %v", err)
	}
	plain := errors.New("sdk detail with secret")
	err := safeError(t.Context(), plain)
	if err == nil || err.Error() != "publication destination operation failed" || errors.Is(err, plain) {
		t.Fatalf("live ctx: %v", err)
	}
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if err = safeError(gone, plain); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: %v", err)
	}
}

func TestHangStreamReadHonoursContext(t *testing.T) {
	body := &hangReader{r: bytes.NewReader([]byte("abc")), limit: 10}
	s := &stream{ctx: t.Context(), body: io.NopCloser(body), remaining: 3}
	p := make([]byte, 8)
	if n, err := s.Read(p); n != 3 || err != nil || string(p[:n]) != "abc" {
		t.Fatalf("live read: %d %v", n, err)
	}
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	body = &hangReader{r: bytes.NewReader([]byte("abc")), limit: 10}
	s = &stream{ctx: gone, body: io.NopCloser(body), remaining: 3}
	if n, err := s.Read(p); n != 0 || !errors.Is(err, context.Canceled) || body.reads != 0 {
		t.Fatalf("cancelled read: %d %v, body reads %d", n, err, body.reads)
	}
}
