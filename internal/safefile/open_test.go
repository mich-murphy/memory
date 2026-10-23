package safefile

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func canonicalTemp(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpenResolvedBindsEveryComponent(t *testing.T) {
	root := canonicalTemp(t)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sub, "asset")
	if err := os.WriteFile(p, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := OpenResolved(t.Context(), p, st)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if string(b) != "safe" {
		t.Fatal("wrong body")
	}
	if err := os.Rename(sub, sub+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sub+"-old", sub); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenResolved(t.Context(), p, st); err == nil {
		_ = f.Close()
		t.Fatal("ancestor alias followed")
	}
	if err := os.Remove(sub); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(sub+"-old", sub); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p, p+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenResolved(t.Context(), p, st); err == nil {
		_ = f.Close()
		t.Fatal("identity replacement accepted")
	}
}

func TestOpenFailures(t *testing.T) {
	root := canonicalTemp(t)
	for _, p := range []string{"relative", root + "/../unclean", filepath.Join(root, "missing"), root} {
		if f, err := OpenResolved(t.Context(), p, nil); err == nil {
			_ = f.Close()
			t.Fatal("unsafe path accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := OpenDirectory(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
