package store_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/store"
)

func catalog(t *testing.T, bucket store.Store) {
	t.Helper()
	ctx := t.Context()
	for _, key := range []string{"prefix/c", "prefix/a", "other", "prefix/b"} {
		if err := bucket.Put(ctx, key, []byte(key), nil); err != nil {
			t.Fatal(err)
		}
	}
	var keys []string
	token := ""
	for {
		page, err := bucket.List(ctx, "prefix/", token, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, obj := range page.Objects {
			keys = append(keys, obj.Key)
		}
		if page.Next == "" {
			break
		}
		if token == page.Next {
			t.Fatal("List repeated cursor")
		}
		token = page.Next
	}
	if got := strings.Join(keys, ","); got != "prefix/a,prefix/b,prefix/c" {
		t.Fatalf("List = %s", got)
	}
	if _, err := bucket.Head(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Head missing = %v", err)
	}
	reader, err := bucket.OpenReader(ctx, "prefix/a")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(body) != "prefix/a" {
		t.Fatalf("OpenReader = %q", body)
	}
	if err := bucket.Create(ctx, "identity", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := bucket.Create(ctx, "identity", []byte("second")); !errors.Is(err, store.ErrExists) {
		t.Fatalf("Create existing = %v", err)
	}
	got, err := bucket.Get(ctx, "identity")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("Create overwrote: %q", got)
	}
}

func TestLocalCatalog(t *testing.T) { catalog(t, store.NewLocal(t.TempDir())) }
