package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "f.txt")
	if err := WriteFileAtomic(path, []byte("one"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "two" {
		t.Errorf("read %q, %v", data, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp files left: %v", entries)
	}
	failing := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("read failed")))
	if err := WriteAtomic(path, failing, 0o600); err == nil {
		t.Error("WriteAtomic succeeded with a failing reader")
	}
	if data, _ := os.ReadFile(path); string(data) != "two" {
		t.Errorf("a failed write left %q", data)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("a failed write left temp files: %v", entries)
	}
	if err := WriteFileAtomic(filepath.Join(path, "under-a-file"), nil, 0o600); err == nil {
		t.Error("wrote under a regular file")
	}
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		path, root string
		want       bool
	}{
		{"/a/b", "/a", true}, {"/a", "/a", true}, {"/ab", "/a", false}, {"/a/../b", "/a", false}, {"rel", "/a", false},
	} {
		if got := Within(c.path, c.root); got != c.want {
			t.Errorf("Within(%q, %q) = %v", c.path, c.root, got)
		}
	}
}
