package capturepolicy

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type countFS struct{ lstat, readlink map[string]int }

func newCountFS() *countFS { return &countFS{map[string]int{}, map[string]int{}} }

func (c *countFS) Lstat(p string) (os.FileInfo, error) { c.lstat[p]++; return os.Lstat(p) }

func (c *countFS) Readlink(p string) (string, error) { c.readlink[p]++; return os.Readlink(p) }

func resolveFixture(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if again, err := filepath.EvalSymlinks(d); err != nil || again != d {
		t.Fatalf("base %q is not symlink-free", d)
	}
	for _, dir := range []string{"a/b", "dir", "noperm"} {
		if err := os.MkdirAll(filepath.Join(d, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "a", "b", "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "noperm", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"a/link": "b", "a/abslink": filepath.Join(d, "a", "b"), "a/b/up": "..", "dir/sym": "../a",
		"c1": "c2", "c2": "c1", "self": "self", "dangling": "nowhere",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(d, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(d, "noperm"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(d, "noperm"), 0o700) })
	return d
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Error() == b.Error()
}

func TestResolveWithMemoMatchesResolve(t *testing.T) {
	d := resolveFixture(t)
	count := newCountFS()
	memo := NewFSMemoOver(count)
	pool := []string{"a", "b", "link", "abslink", "up", "dir", "sym", "c1", "c2", "self", "dangling", "file", "missing", "noperm", "x", "..", ".", "a", "b"}
	rng := rand.New(rand.NewPCG(7, 11))
	paths := []string{d, filepath.Join(d, "a", "link", "..", "b", "file"), filepath.Join(d, "a", "b", "file", "x"), filepath.Join(d, "missing", "y")}
	for range 800 {
		n := 1 + rng.IntN(7)
		parts := []string{d}
		for range n {
			parts = append(parts, pool[rng.IntN(len(pool))])
		}
		paths = append(paths, strings.Join(parts, string(filepath.Separator)))
	}
	for _, p := range paths {
		want, wantErr := Resolve(p)
		got, gotErr := memo.Resolve(p)
		if diff := cmp.Diff(want, got); diff != "" || !sameErr(wantErr, gotErr) {
			t.Fatalf("path %q: diff (-live +memo) %s; errors live=%v memo=%v", strings.TrimPrefix(p, d), diff, wantErr, gotErr)
		}
		again, againErr := memo.Resolve(p)
		if diff := cmp.Diff(got, again); diff != "" || !sameErr(gotErr, againErr) {
			t.Fatalf("path %q: second memo resolve differs: %s", strings.TrimPrefix(p, d), diff)
		}
	}
	for p, n := range count.lstat {
		if n != 1 {
			t.Errorf("Lstat(%s) = %d calls, want 1", strings.TrimPrefix(p, d), n)
		}
	}
	for p, n := range count.readlink {
		if n != 1 {
			t.Errorf("Readlink(%s) = %d calls, want 1", strings.TrimPrefix(p, d), n)
		}
	}
	if len(count.lstat) == 0 || len(count.readlink) == 0 {
		t.Fatal("fixture exercised no Lstat/Readlink")
	}
}

func TestResolveWithMemoErrorClasses(t *testing.T) {
	d := resolveFixture(t)
	memo := NewFSMemo()
	r, err := memo.Resolve(filepath.Join(d, "a", "link", "..", "b", "file"))
	if err != nil || r.Path != filepath.Join(d, "a", "b", "file") {
		t.Fatalf("resolve through symlink and ..: %q %v", r.Path, err)
	}
	cases := []struct {
		rel  string
		is   error
		text string
	}{
		{"missing/y", os.ErrNotExist, ""},
		{"dangling", os.ErrNotExist, ""},
		{"a/b/file/x", syscall.ENOTDIR, ""},
		{"c1", nil, "cyclic or excessive"},
		{"self", nil, "cyclic or excessive"},
	}
	for _, c := range cases {
		for range 2 {
			_, err := memo.Resolve(filepath.Join(d, c.rel))
			if err == nil || (c.is != nil && !errors.Is(err, c.is)) || !strings.Contains(err.Error(), c.text) {
				t.Errorf("%s: error %v, want is=%v text=%q", c.rel, err, c.is, c.text)
			}
		}
	}
	if os.Geteuid() != 0 {
		if _, err := memo.Resolve(filepath.Join(d, "noperm", "x")); !errors.Is(err, os.ErrPermission) {
			t.Errorf("unreadable dir: error %v, want permission", err)
		}
	}
}

// A memo is a snapshot of one call: a fresh memo observes a replaced directory,
// and plain Resolve is never affected by an earlier memo.
func TestFSMemoFreshMemoSeesReplacedDirectory(t *testing.T) {
	d := resolveFixture(t)
	target := filepath.Join(d, "a", "b", "file")
	old := NewFSMemo()
	if r, err := old.Resolve(filepath.Join(d, "dir", "f")); !errors.Is(err, os.ErrNotExist) || len(r.Identities) == 0 {
		t.Fatalf("setup resolve: %v", err)
	}
	if err := os.Remove(filepath.Join(d, "dir", "sym")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(d, "a", "b"), filepath.Join(d, "dir")); err != nil {
		t.Fatal(err)
	}
	r, err := NewFSMemo().Resolve(filepath.Join(d, "dir", "file"))
	if err != nil || r.Path != target {
		t.Fatalf("fresh memo: %q %v, want %q", r.Path, err, target)
	}
	live, err := Resolve(filepath.Join(d, "dir", "file"))
	if err != nil || live.Path != target {
		t.Fatalf("live: %q %v", live.Path, err)
	}
}
