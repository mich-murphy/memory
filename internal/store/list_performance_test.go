package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// naiveList is deliberately the old full-bucket algorithm, without its repeated
// sorts. It is independent of the optimized directory intersection and heap.
func naiveList(root, prefix, token string, limit int) (Page, error) {
	var objects []Object
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == filepath.Join(root, ".store-staging") {
				return fs.SkipDir
			}
			return nil
		}
		key, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key = filepath.ToSlash(key)
		if !strings.HasPrefix(key, prefix) || key <= token {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		objects = append(objects, Object{Key: key, Size: info.Size()})
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return Page{}, nil
	}
	if err != nil {
		return Page{}, err
	}
	slices.SortFunc(objects, func(a, b Object) int { return strings.Compare(a.Key, b.Key) })
	page := Page{Objects: objects}
	if len(objects) > limit {
		page.Objects = objects[:limit]
		page.Next = objects[limit-1].Key
	}
	return page, nil
}

func TestLocalListMatchesNaive(t *testing.T) {
	root := t.TempDir()
	keys := []string{"a/x", "a-b", "a.b", "a0", "foo/a", "foo/deep/x", "foobar/x", "foo!", "foo.bar/x", "z/x", ".store-staging/hidden", "nested/.store-staging/visible", "odd/λ/x", "odd/space name", "odd/back\\slash"}
	for i := range 1005 {
		keys = append(keys, fmt.Sprintf("many/%04d", i))
	}
	for _, key := range keys {
		path := filepath.Join(root, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	local := NewLocal(root)
	for _, prefix := range []string{"", "a", "a/", "foo", "foo/", "foo/de", "foobar/", "many/", "nested/", "odd/", "missing/", "../", "/foo/", "foo//", "foo/../", "foo/./", "foo\x00/", "odd/back\\"} {
		for _, token := range []string{"", "a-b", "foo", "foo/deep/w", "foobar/z", "many/0500", "zz"} {
			for _, limit := range []int{1, 7, 1000} {
				got, err := local.List(t.Context(), prefix, token, limit)
				if err != nil {
					t.Fatal(err)
				}
				want, err := naiveList(root, prefix, token, limit)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.EqualFunc(got.Objects, want.Objects, func(a, b Object) bool { return reflect.DeepEqual(a, b) }) || got.Next != want.Next {
					t.Fatalf("prefix=%q token=%q limit=%d: got %+v, want %+v", prefix, token, limit, got, want)
				}
			}
		}
	}
	for _, prefix := range []string{"a", "foo", "many/"} {
		var all []Object
		for token := ""; ; {
			page, err := local.List(t.Context(), prefix, token, 7)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, page.Objects...)
			if page.Next == "" {
				break
			}
			if page.Next <= token {
				t.Fatal("pagination did not advance")
			}
			token = page.Next
		}
		want, err := naiveList(root, prefix, "", 10000)
		if err != nil || !reflect.DeepEqual(all, want.Objects) {
			t.Fatalf("complete pagination mismatch for %q: %v", prefix, err)
		}
	}
}

func TestLocalListSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"linkdir": outside, "linkfile": filepath.Join(outside, "secret"), "broken": filepath.Join(outside, "missing")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, prefix := range []string{"", "linkdir", "linkdir/", "linkdir/secret", "linkfile", "broken"} {
		got, err := NewLocal(root).List(t.Context(), prefix, "", 1000)
		if err != nil {
			t.Fatal(err)
		}
		want, err := naiveList(root, prefix, "", 1000)
		if err != nil || !slices.EqualFunc(got.Objects, want.Objects, func(a, b Object) bool { return reflect.DeepEqual(a, b) }) || got.Next != want.Next {
			t.Fatalf("symlink semantics changed for %q: got %+v want %+v (%v)", prefix, got, want, err)
		}
	}
}

func TestLocalListCancellationLimitsAndVanishedRoot(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := local.List(ctx, "", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled List: %v", err)
	}
	for _, limit := range []int{-1, 0, 1001} {
		if _, err := local.List(t.Context(), "", "", limit); err == nil {
			t.Fatalf("accepted limit %d", limit)
		}
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{t.Context(), ctx} {
		page, err := local.List(ctx, "../", "", 1)
		if err != nil || len(page.Objects) != 0 || page.Next != "" {
			t.Fatalf("vanished root: %+v, %v", page, err)
		}
	}
}

func TestLocalListPrunesIrrelevantDirectories(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(root)
	// SkipDir is the WalkDir seam: returning it before ReadDir prevents any
	// traversal of that directory. No permission assumptions or timing checks.
	for _, tc := range []struct {
		key, prefix, token string
		skip               bool
	}{
		{"foo", "foo/", "", false},
		{"foobar", "foo", "", false},
		{"foobar", "foo/", "", true},
		{"unrelated", "foo/", "", true},
		{"foo", "foo", "foo0", true},
		{"foo", "foo", "foo/z", false},
		{"foo", "foo", "a", false},
		{"foo", "../", "", true},
		{".store-staging", "", "", true},
	} {
		entry := listDirectoryEntry{name: tc.key}
		_, err := local.listObject(filepath.Join(root, tc.key), entry, tc.prefix, tc.token)
		if errors.Is(err, fs.SkipDir) != tc.skip {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

type listDirectoryEntry struct{ name string }

func (e listDirectoryEntry) Name() string             { return e.name }
func (listDirectoryEntry) IsDir() bool                { return true }
func (listDirectoryEntry) Type() fs.FileMode          { return fs.ModeDir }
func (listDirectoryEntry) Info() (fs.FileInfo, error) { panic("directory metadata must not be read") }

func TestListCandidatesBounded(t *testing.T) {
	for _, capacity := range []int{2, 1001} {
		var candidates listCandidates
		for i := 4000; i >= 0; i-- {
			candidates.keep(Object{Key: fmt.Sprintf("%04d", i)}, capacity)
			if len(candidates) > capacity {
				t.Fatalf("retained %d candidates, capacity %d", len(candidates), capacity)
			}
			for child := 1; child < len(candidates); child++ {
				if candidates[(child-1)/2].Key < candidates[child].Key {
					t.Fatal("max heap invariant lost")
				}
			}
		}
		slices.SortFunc(candidates, func(a, b Object) int { return strings.Compare(a.Key, b.Key) })
		for i, object := range candidates {
			if object.Key != fmt.Sprintf("%04d", i) {
				t.Fatalf("candidate %d: %q", i, object.Key)
			}
		}
	}
}
