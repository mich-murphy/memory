package safefile

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWalkStopsBeforeEnumeratingLargeDirectory(t *testing.T) {
	root := canonicalTemp(t)
	for i := range 600 {
		if err := os.WriteFile(filepath.Join(root, strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		reads, enumerated, visits := 0, 0, 0
		err := walk(ctx, root, 140, 8, func(string, fs.DirEntry, error) error { visits++; return nil }, func(f *os.File, n int) ([]fs.DirEntry, error) {
			reads++
			if n > 128 {
				t.Fatal("unbounded batch")
			}
			d, e := f.ReadDir(n)
			enumerated += len(d)
			if canceled {
				cancel()
			}
			return d, e
		})
		cancel()
		if err == nil || enumerated > 139 || enumerated >= 600 || reads > 2 {
			t.Fatalf("limit: reads=%d enumerated=%d visits=%d err=%v", reads, enumerated, visits, err)
		}
		if canceled && (!errors.Is(err, context.Canceled) || reads != 1) {
			t.Fatal("cancellation did not stop between batches", err)
		}
	}
}

func TestWalkDepthPruneAndErrors(t *testing.T) {
	root := canonicalTemp(t)
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	visit := func(_ string, _ fs.DirEntry, err error) error { return err }
	if err := Walk(t.Context(), root, 100, 1, visit); err == nil {
		t.Fatal("depth ignored")
	}
	count := 0
	if err := Walk(t.Context(), root, 100, 8, func(_ string, d fs.DirEntry, err error) error {
		count++
		if d.Name() == "a" {
			return filepath.SkipDir
		}
		return err
	}); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if err := Walk(t.Context(), root, 100, 8, visit); err != nil {
		t.Fatal(err)
	}
	if err := Walk(t.Context(), root+"/missing", 100, 8, visit); err == nil {
		t.Fatal("missing hidden")
	}
	if err := walk(t.Context(), root, 100, 8, visit, func(*os.File, int) ([]fs.DirEntry, error) { return nil, errors.New("injected enumeration error") }); err == nil {
		t.Fatal("read error hidden")
	}
}
