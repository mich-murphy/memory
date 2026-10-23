package sessioncapture

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type partialQuotaSink struct{}

func (partialQuotaSink) Write([]byte) (int, error) { return 3, io.ErrUnexpectedEOF }

func TestQuotaPartialWriteAndDynamicReservations(t *testing.T) {
	q := &Queue{opts: Options{MaxSpoolBytes: 10}}
	a := &quotaWriter{q: q, w: partialQuotaSink{}}
	if n, err := a.Write(make([]byte, 8)); n != 3 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial write %d %v", n, err)
	}
	if q.used != 3 || q.writing != 0 {
		t.Fatalf("partial charge used=%d writing=%d", q.used, q.writing)
	}
	var sink bytes.Buffer
	b := &quotaWriter{q: q, w: &sink}
	if _, err := b.Write(make([]byte, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Write(make([]byte, 1)); !errors.Is(err, ErrQuota) {
		t.Fatalf("overshoot: %v", err)
	}
	if sink.Len() != 7 || q.used != 10 {
		t.Fatal("quota not exact")
	}
}

func TestQuotaRetainsFailedCleanupAndRestartOrphan(t *testing.T) {
	dir, o, r := setup(t)
	injected := errors.New("injected")
	o.Fault = func(stage string) error {
		if stage == "payload-before-sync" || stage == "payload-before-cleanup" {
			return injected
		}
		return nil
	}
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	used, err := q.usage()
	if err != nil {
		t.Fatal(err)
	}
	if used == 0 || used != q.used {
		t.Fatalf("failed cleanup undercharged physical=%d accounted=%d", used, q.used)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	o.Fault = nil
	o.MaxSpoolBytes = used - 1
	if bad, err := Open(t.Context(), dir, o); !errors.Is(err, ErrQuota) {
		if bad != nil {
			_ = bad.Close()
		}
		t.Fatalf("startup accepted over-quota orphan: %v", err)
	}
	o.MaxSpoolBytes = used
	q = open(t, dir, o)
	if q.used != used {
		t.Fatalf("restart discarded orphan: %d != %d", q.used, used)
	}
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrQuota) {
		t.Fatalf("orphan not charged: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("orphan removed or failed capture leaked temporary")
	}
}

func TestQuotaStartupIntegrityAndReservedFreeSpace(t *testing.T) {
	dir, o, r := setup(t)
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "payloads", "unexpected")
	if err := os.Mkdir(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if badq, err := Open(t.Context(), dir, o); !errors.Is(err, ErrIntegrity) {
		if badq != nil {
			_ = badq.Close()
		}
		t.Fatalf("startup missed malformed spool: %v", err)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	o.MinFreeBytes = 1<<63 - 1
	q = open(t, dir, o)
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrQuota) {
		t.Fatalf("reserved free space ignored: %v", err)
	}
	if q.used != 0 || q.writing != 0 {
		t.Fatal("failed reservation leaked charge")
	}
}

func TestQuotaDuplicateTemporaryCleanupFailure(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	first := capture(t, q, r)
	size := first.Sources[0].Object.EncodedSize
	q.opts.MaxSpoolBytes = 2 * size
	q.opts.Fault = func(stage string) error {
		if stage == "payload-before-cleanup" {
			return errors.New("cleanup unavailable")
		}
		return nil
	}
	second := capture(t, q, r)
	if second.Sequence != 2 {
		t.Fatal("duplicate capture did not retain legacy ordering")
	}
	used, err := q.usage()
	if err != nil {
		t.Fatal(err)
	}
	if used != 2*size || q.used != used {
		t.Fatalf("duplicate temporary peak was discounted: physical=%d accounted=%d", used, q.used)
	}
	q.opts.Fault = nil
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrQuota) {
		t.Fatalf("duplicate orphan not charged: %v", err)
	}
	if _, err := q.CaptureIfChanged(t.Context(), r); err != nil {
		t.Fatalf("owned unchanged bytes blocked by orphan quota: %v", err)
	}
}
