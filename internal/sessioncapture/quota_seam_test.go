package sessioncapture

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func fixedFree(free int64) func(string) (int64, error) {
	return func(string) (int64, error) { return free, nil }
}

func TestReserveFreeSpaceBoundaries(t *testing.T) {
	const minFree = 100
	for _, test := range []struct {
		name string
		free int64
		n    int64
		want error
	}{
		{"free equals reserve admits zero bytes", minFree, 0, nil},
		{"free equals reserve refuses one byte", minFree, 1, ErrQuota},
		{"free below reserve refuses zero bytes", minFree - 1, 0, ErrQuota},
		{"n equals free minus reserve", minFree + 50, 50, nil},
		{"n exceeds free minus reserve by one", minFree + 50, 51, ErrQuota},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := &Queue{opts: Options{MaxSpoolBytes: 1 << 40, MinFreeBytes: minFree}, freeBytes: fixedFree(test.free)}
			err := q.reserve(test.n)
			if !errors.Is(err, test.want) || (test.want == nil && err != nil) {
				t.Fatalf("reserve(%d) free=%d: %v want %v", test.n, test.free, err, test.want)
			}
			wantUsed := int64(0)
			if test.want == nil {
				wantUsed = test.n
			}
			if q.used != wantUsed || q.writing != wantUsed {
				t.Fatalf("charge used=%d writing=%d want %d", q.used, q.writing, wantUsed)
			}
		})
	}
}

func TestReserveSubtractsOutstandingWrites(t *testing.T) {
	q := &Queue{opts: Options{MaxSpoolBytes: 1 << 40, MinFreeBytes: 10}, freeBytes: fixedFree(30)}
	// 20 bytes are usable. statfs does not yet reflect the first reservation.
	if err := q.reserve(12); err != nil {
		t.Fatal(err)
	}
	if err := q.reserve(9); !errors.Is(err, ErrQuota) {
		t.Fatalf("outstanding write ignored: %v", err)
	}
	if err := q.reserve(8); err != nil {
		t.Fatalf("exact remainder refused: %v", err)
	}
	if q.used != 20 || q.writing != 20 {
		t.Fatalf("used=%d writing=%d", q.used, q.writing)
	}
	if err := q.reserve(1); !errors.Is(err, ErrQuota) {
		t.Fatalf("over remainder: %v", err)
	}
	// A finished write is reflected by statfs, so it no longer counts as outstanding.
	q.writing = 0
	q.freeBytes = fixedFree(11)
	if err := q.reserve(1); err != nil {
		t.Fatalf("settled writes still subtracted: %v", err)
	}
}

func TestReserveFreeSpaceProbeErrorAndDisabledReserve(t *testing.T) {
	probe := errors.New("statfs failed")
	q := &Queue{opts: Options{MaxSpoolBytes: 100, MinFreeBytes: 1}, freeBytes: func(string) (int64, error) { return 0, probe }}
	if err := q.reserve(1); !errors.Is(err, probe) || q.used != 0 || q.writing != 0 {
		t.Fatalf("probe error: %v used=%d writing=%d", err, q.used, q.writing)
	}
	called := false
	q = &Queue{opts: Options{MaxSpoolBytes: 100}, freeBytes: func(string) (int64, error) { called = true; return 0, nil }}
	if err := q.reserve(5); err != nil || called {
		t.Fatalf("zero reserve must not probe: %v called=%v", err, called)
	}
}

func TestReserveQuotaBoundaryAtFullSpool(t *testing.T) {
	q := &Queue{opts: Options{MaxSpoolBytes: 10}, used: 10}
	// A zero-byte write needs no space, so a spool filled exactly to the limit admits it.
	if err := q.reserve(0); err != nil {
		t.Fatalf("zero bytes at exactly full: %v", err)
	}
	if err := q.reserve(1); !errors.Is(err, ErrQuota) {
		t.Fatalf("one byte at full: %v", err)
	}
	q.used = 11
	if err := q.reserve(0); !errors.Is(err, ErrQuota) {
		t.Fatalf("over-full spool: %v", err)
	}
}

func TestSweepTemporariesOnOpen(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	capture(t, q, r)
	kept, err := os.ReadDir(filepath.Join(dir, "payloads"))
	if err != nil || len(kept) != 1 {
		t.Fatalf("setup payloads: %v %d", err, len(kept))
	}
	usedBefore := q.used
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	payloads := filepath.Join(dir, "payloads")
	plant := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plant(filepath.Join(payloads, "capture-12345"), strings.Repeat("x", 100))
	plant(filepath.Join(payloads, "capture-empty"), "")
	// Not writer temporaries: a durable-looking payload with the temp prefix and an unrelated name.
	plant(filepath.Join(payloads, "capture-keep.zst"), "durable")
	plant(filepath.Join(dir, "capture-outside"), "outside payloads")
	plant(filepath.Join(dir, "note.txt"), "other")

	q2 := open(t, dir, o)
	wantUsed := usedBefore + int64(len("durable"))
	if q2.used != wantUsed {
		t.Fatalf("accounted %d want %d (temporaries must release their bytes)", q2.used, wantUsed)
	}
	phys, err := q2.usage()
	if err != nil || phys != wantUsed {
		t.Fatalf("physical usage %d %v want %d", phys, err, wantUsed)
	}
	var names []string
	es, err := os.ReadDir(payloads)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		names = append(names, e.Name())
	}
	want := []string{"capture-keep.zst", kept[0].Name()}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("payloads after Open (-want +got):\n%s", diff)
	}
	for _, outside := range []string{"capture-outside", "note.txt"} {
		if _, err := os.Stat(filepath.Join(dir, outside)); err != nil {
			t.Fatalf("%s outside payloads was touched: %v", outside, err)
		}
	}
}

func TestSweepTemporariesIgnoresNonRegularAndReportsNothingToDo(t *testing.T) {
	dir := t.TempDir()
	payloads := filepath.Join(dir, "payloads")
	if err := os.Mkdir(payloads, 0o700); err != nil {
		t.Fatal(err)
	}
	removed, err := sweepTemporaries(dir)
	if err != nil || removed {
		t.Fatalf("empty payloads: %v %v", removed, err)
	}
	if err := os.Mkdir(filepath.Join(payloads, "capture-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(payloads, "capture-link")); err != nil {
		t.Fatal(err)
	}
	removed, err = sweepTemporaries(dir)
	if err != nil || removed {
		t.Fatalf("non-regular entries must be left for the integrity check: %v %v", removed, err)
	}
	if _, err := sweepTemporaries(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing payloads directory must be an error")
	}
}
