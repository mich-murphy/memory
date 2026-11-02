package sessioncapture

import (
	"errors"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

type fuzzSink struct {
	short int
	err   error
}

func (w fuzzSink) Write(_ []byte) (int, error) { return w.short, w.err }

func FuzzQuotaWriter(f *testing.F) {
	M := publication.MaxEncodedObjectBytes
	steps := []byte{10, 0, 0, 0, 0, 1, 0, 0, 5, 0, 3, 0, 7, 0, 0, 1}
	f.Add(int64(1<<20), int64(0), int64(0), int64(0), steps)
	f.Add(int64(1<<20), int64(1<<20-5), int64(0), int64(0), steps)
	f.Add(int64(1<<20), int64(0), int64(0), M-1, []byte{1, 0, 0, 0, 1, 0, 0, 0})
	f.Add(int64(1<<20), int64(0), int64(0), M, []byte{1, 0, 0, 0})
	f.Add(int64(1<<20), int64(0), int64(0), M+1, []byte{1, 0, 0, 0})
	f.Add(int64(1<<20), int64(0), int64(0), int64(-1), []byte{1, 0, 0, 0})
	f.Add(int64(1<<62), int64(1<<62), int64(0), int64(0), []byte{1, 0, 0, 0})
	f.Add(int64(100), int64(5), int64(9), int64(0), []byte{1, 0, 0, 0})
	f.Add(int64(1<<63-1), int64(1<<63-1), int64(0), M-1, []byte{255, 255, 0, 0})
	f.Fuzz(func(t *testing.T, maxSpool, used0, writing0, bytes0 int64, steps []byte) {
		if maxSpool <= 0 {
			t.Skip()
		}
		q := &Queue{opts: Options{MaxSpoolBytes: maxSpool}, used: used0, writing: writing0}
		wr := &quotaWriter{q: q, bytes: bytes0}
		wantUsed, wantBytes := used0, bytes0
		boom := errors.New("boom")
		for len(steps) >= 4 {
			size := int64(steps[0]) | int64(steps[1])<<8
			short := int(size)
			if steps[2] != 0 {
				short = int(steps[2]) % int(size+1)
			}
			var werr error
			if steps[3]&1 != 0 {
				werr = boom
			}
			steps = steps[4:]
			wr.w = fuzzSink{short, werr}
			var want error
			write := false
			switch {
			case wantBytes < 0 || wantBytes > publication.MaxEncodedObjectBytes:
				want = ErrIntegrity
			case size > publication.MaxEncodedObjectBytes-wantBytes:
				want = ErrEncodedSize
			case wantUsed < 0 || q.writing < 0 || q.writing > wantUsed:
				want = ErrIntegrity
			case wantUsed > maxSpool || size > maxSpool-wantUsed:
				want = ErrQuota
			default:
				write = true
			}
			writingBefore := q.writing
			n, err := wr.Write(make([]byte, size))
			if !write {
				if n != 0 || !errors.Is(err, want) {
					t.Fatalf("n=%d err=%v want %v", n, err, want)
				}
				if errors.Is(want, ErrQuota) && !errors.Is(want, ErrEncodedSize) && errors.Is(err, ErrEncodedSize) {
					t.Fatal("quota misclassified as encoded size")
				}
				if errors.Is(want, ErrEncodedSize) && !errors.Is(err, ErrQuota) {
					t.Fatal("encoded size must wrap ErrQuota")
				}
				if errors.Is(want, ErrIntegrity) && errors.Is(err, ErrQuota) {
					t.Fatal("integrity misclassified as quota")
				}
			} else {
				if n != short {
					t.Fatalf("n=%d want %d", n, short)
				}
				switch {
				case werr != nil:
					if !errors.Is(err, werr) {
						t.Fatalf("err %v", err)
					}
				case short < int(size):
					if err.Error() != "short write" {
						t.Fatalf("err %v", err)
					}
				case err != nil:
					t.Fatalf("err %v", err)
				}
				wantUsed += int64(short)
				wantBytes += int64(short)
			}
			if q.used != wantUsed || wr.bytes != wantBytes || q.writing != writingBefore {
				t.Fatalf("used=%d/%d bytes=%d/%d writing=%d/%d", q.used, wantUsed, wr.bytes, wantBytes, q.writing, writingBefore)
			}
			if write && (q.used > maxSpool || wr.bytes > publication.MaxEncodedObjectBytes) {
				t.Fatalf("limit exceeded used=%d bytes=%d", q.used, wr.bytes)
			}
		}
	})
}
