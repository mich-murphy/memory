package sessioncapture

import (
	"errors"
	"io"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

type envelopeTailWriter struct {
	calls int
	n     int
	err   error
}

func (w *envelopeTailWriter) Write([]byte) (int, error) {
	w.calls++
	return w.n, w.err
}

func TestEncodedSizeBoundaryBeforeReservation(t *testing.T) {
	limit := publication.MaxEncodedObjectBytes
	if limit != 1<<30 {
		t.Fatal("production encoded envelope changed")
	}
	if !errors.Is(ErrEncodedSize, ErrQuota) || errors.Is(ErrQuota, ErrEncodedSize) {
		t.Fatal("encoded and aggregate quota classification")
	}
	initial := limit - 1 + 17 // Writer's existing bytes plus unrelated spool bytes.
	q := &Queue{opts: Options{MaxSpoolBytes: 2 * limit}, used: initial}
	dst := &envelopeTailWriter{n: 1}
	w := &quotaWriter{q: q, w: dst, bytes: limit - 1}
	if n, err := w.Write([]byte{1}); n != 1 || err != nil {
		t.Fatalf("exact cap: n=%d err=%v", n, err)
	}
	if w.bytes != limit || q.used != initial+1 || q.writing != 0 || dst.calls != 1 {
		t.Fatal("exact cap accounting")
	}
	if n, err := w.Write([]byte{2}); n != 0 || !errors.Is(err, ErrEncodedSize) {
		t.Fatalf("cap+1: n=%d err=%v", n, err)
	}
	if w.bytes != limit || q.used != initial+1 || q.writing != 0 || dst.calls != 1 {
		t.Fatal("rejection mutated writer/reservation/counters")
	}
	// Aggregate quota failures remain distinguishable from encoded-envelope failure.
	q.opts.MaxSpoolBytes = q.used
	w.bytes = 0 // A fresh writer sharing the already-full spool.
	if n, err := w.Write([]byte{3}); n != 0 || !errors.Is(err, ErrQuota) || errors.Is(err, ErrEncodedSize) {
		t.Fatalf("aggregate quota: n=%d err=%v", n, err)
	}
	if q.used != initial+1 || q.writing != 0 || dst.calls != 1 || w.bytes != 0 {
		t.Fatal("aggregate rejection mutation")
	}
}

func TestEncodedSizeInvalidCounterAndPartialWrite(t *testing.T) {
	limit := publication.MaxEncodedObjectBytes
	for _, count := range []int64{-1, limit + 1} {
		initial := max(count, 0) + 17
		q := &Queue{opts: Options{MaxSpoolBytes: 2 * limit}, used: initial}
		dst := &envelopeTailWriter{n: 1}
		w := &quotaWriter{q: q, w: dst, bytes: count}
		if n, err := w.Write([]byte{1}); n != 0 || !errors.Is(err, ErrIntegrity) {
			t.Fatalf("invalid counter %d: n=%d err=%v", count, n, err)
		}
		if w.bytes != count || q.used != initial || q.writing != 0 || dst.calls != 0 {
			t.Fatal("invalid counter touched accounting/writer")
		}
	}
	failure := errors.New("synthetic write failure")
	for _, writeErr := range []error{nil, failure} {
		initial := limit - 2 + 17
		q := &Queue{opts: Options{MaxSpoolBytes: 2 * limit}, used: initial}
		dst := &envelopeTailWriter{n: 1, err: writeErr}
		w := &quotaWriter{q: q, w: dst, bytes: limit - 2}
		wantErr := writeErr
		if wantErr == nil {
			wantErr = io.ErrShortWrite
		}
		if n, err := w.Write([]byte{1, 2}); n != 1 || !errors.Is(err, wantErr) {
			t.Fatalf("partial write: n=%d err=%v", n, err)
		}
		if w.bytes != limit-1 || q.used != initial+1 || q.writing != 0 || dst.calls != 1 {
			t.Fatal("partial-write accounting changed")
		}
	}
}
