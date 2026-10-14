package store_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/store"
)

type namespaceProbe struct {
	store.Store
	calls      []string
	page       *store.Page
	head       *store.Object
	fail       error
	deleteFail string
}

func (p *namespaceProbe) List(ctx context.Context, prefix, token string, limit int) (store.Page, error) {
	p.calls = append(p.calls, "list "+prefix)
	if p.page != nil {
		return *p.page, p.fail
	}
	return p.Store.List(ctx, prefix, token, limit)
}

func (p *namespaceProbe) Head(ctx context.Context, key string) (store.Object, error) {
	p.calls = append(p.calls, "head "+key)
	if p.head != nil {
		return *p.head, p.fail
	}
	return p.Store.Head(ctx, key)
}

func (p *namespaceProbe) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	p.calls = append(p.calls, "reader "+key)
	return p.Store.OpenReader(ctx, key)
}

func (p *namespaceProbe) Create(ctx context.Context, key string, body []byte) error {
	p.calls = append(p.calls, "create "+key)
	return p.Store.Create(ctx, key, body)
}

func (p *namespaceProbe) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	p.calls = append(p.calls, "put "+key)
	return p.Store.Put(ctx, key, body, meta)
}

func (p *namespaceProbe) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	p.calls = append(p.calls, "file "+key)
	return p.Store.PutFile(ctx, key, path, meta)
}

func (p *namespaceProbe) Get(ctx context.Context, key string) ([]byte, error) {
	p.calls = append(p.calls, "get "+key)
	return p.Store.Get(ctx, key)
}

func (p *namespaceProbe) Copy(ctx context.Context, src, dst, sha string) (bool, error) {
	p.calls = append(p.calls, "copy "+src, "copy "+dst)
	return p.Store.Copy(ctx, src, dst, sha)
}

func (p *namespaceProbe) Delete(ctx context.Context, key string) error {
	p.calls = append(p.calls, "delete "+key)
	if key == p.deleteFail {
		return errors.New("synthetic credential sentinel")
	}
	return p.Store.Delete(ctx, key)
}

func testNamespace(t *testing.T) (*namespace, *namespaceProbe) {
	t.Helper()
	p := &namespaceProbe{Store: store.NewLocal(t.TempDir())}
	n, err := newNamespace(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	return n, p
}

func TestNamespaceRejectsEscapesBeforeAnyStoreCall(t *testing.T) {
	n, p := testNamespace(t)
	for _, key := range []string{"", ".", "..", "../outside", "a/../../outside", "/outside", "a//b", "a/./b", "a/../b", "a\\b", "a\x00b", "bad\xff", n.prefix + "object", "_agent-sessions/test/other/object", "a/"} {
		for _, err := range []error{
			n.Create(t.Context(), key, nil), n.Put(t.Context(), key, nil, nil), n.PutFile(t.Context(), key, "unread", nil), n.Delete(t.Context(), key),
		} {
			if !errors.Is(err, errNamespace) {
				t.Fatal("mutation accepted invalid key")
			}
		}
		if _, err := n.Head(t.Context(), key); !errors.Is(err, errNamespace) {
			t.Fatal("Head accepted invalid key")
		}
		if _, err := n.OpenReader(t.Context(), key); !errors.Is(err, errNamespace) {
			t.Fatal("OpenReader accepted invalid key")
		}
		if _, err := n.Get(t.Context(), key); !errors.Is(err, errNamespace) {
			t.Fatal("Get accepted invalid key")
		}
		if _, err := n.Copy(t.Context(), key, "valid", "hash"); !errors.Is(err, errNamespace) {
			t.Fatal("Copy accepted invalid source")
		}
		if _, err := n.Copy(t.Context(), "valid", key, "hash"); !errors.Is(err, errNamespace) {
			t.Fatal("Copy accepted invalid destination")
		}
	}
	for _, prefix := range []string{"../", "/", "a//", "a/../", "_agent-sessions/test/other/"} {
		if _, err := n.List(t.Context(), prefix, "", 2); !errors.Is(err, errNamespace) {
			t.Fatal("List accepted invalid prefix")
		}
	}
	for _, args := range []struct {
		token string
		limit int
	}{{"unissued", 2}, {"", 0}, {"", 1001}} {
		if _, err := n.List(t.Context(), "", args.token, args.limit); !errors.Is(err, errNamespace) {
			t.Fatal("List accepted invalid token or limit")
		}
	}
	if len(p.calls) != 0 {
		t.Fatalf("underlying calls = %d, want 0", len(p.calls))
	}
}

func TestNamespaceTranslatesEveryOperationAndCleansOnlyOwnedObjects(t *testing.T) {
	n, p := testNamespace(t)
	ctx := t.Context()
	outside := "v3/store.json"
	if err := p.Store.Put(ctx, outside, []byte("existing archive"), nil); err != nil {
		t.Fatal(err)
	}
	encoded := raw.Compress([]byte("source"))
	if err := n.Put(ctx, "v3/store.json", encoded, nil); err != nil {
		t.Fatal(err)
	}
	if err := n.Create(ctx, "two", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := n.Create(ctx, "two", []byte("different")); !errors.Is(err, store.ErrExists) {
		t.Fatalf("Create existing = %v, want ErrExists", err)
	}
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := n.PutFile(ctx, "three", path, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := n.Copy(ctx, "v3/store.json", "four", raw.SHA256([]byte("source"))); err != nil || !ok {
		t.Fatalf("Copy = %v, %v", ok, err)
	}
	obj, err := n.Head(ctx, "two")
	if err != nil || obj.Key != "two" || obj.Size != 8 {
		t.Fatalf("Head = %+v, %v", obj, err)
	}
	body, err := n.Get(ctx, "two")
	if err != nil || string(body) != "original" {
		t.Fatalf("Get = %q, %v", body, err)
	}
	reader, err := n.OpenReader(ctx, "three")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || string(body) != "file" {
		t.Fatalf("OpenReader body = %q, %v", body, err)
	}
	keys, err := n.keys(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"four", "three", "two", "v3/store.json"}, keys); diff != "" {
		t.Fatalf("Keys (-want +got): %s", diff)
	}
	if n.continuationPages() == 0 {
		t.Fatal("small LIST limit did not paginate")
	}
	count, err := n.clean(ctx)
	if err != nil || count != 4 {
		t.Fatalf("Clean = %d, %v, want 4", count, err)
	}
	body, err = p.Store.Get(ctx, outside)
	if err != nil || string(body) != "existing archive" {
		t.Fatal("cleanup altered pre-existing archive")
	}
	for _, call := range p.calls {
		_, key, ok := strings.Cut(call, " ")
		if !ok || !strings.HasPrefix(key, n.prefix) {
			t.Fatalf("unconfined underlying call: %q", call)
		}
	}
}

func TestNamespaceRejectsUntrustedResponsesBeforeStrippingOrCleanup(t *testing.T) {
	n, p := testNamespace(t)
	for _, physical := range []string{"v3/store.json", "_agent-sessions/test/other/object", n.prefix + "../outside", n.prefix + "a//b", n.prefix, n.prefix + "wrong/object"} {
		p.page = &store.Page{Objects: []store.Object{{Key: physical}}}
		page, err := n.List(t.Context(), "requested/", "", 2)
		if !errors.Is(err, errNamespace) || len(page.Objects) != 0 {
			t.Fatal("List exposed untrusted response")
		}
	}
	p.head = &store.Object{Key: "v3/store.json"}
	if _, err := n.Head(t.Context(), "v3/store.json"); !errors.Is(err, errNamespace) {
		t.Fatal("Head exposed foreign key")
	}
	p.page = &store.Page{Objects: []store.Object{{Key: n.prefix + "one"}, {Key: "v3/store.json"}}}
	p.calls = nil
	if _, err := n.clean(t.Context()); !errors.Is(err, errNamespace) {
		t.Fatal("Clean accepted foreign response")
	}
	if len(p.calls) != 1 || !strings.HasPrefix(p.calls[0], "list ") {
		t.Fatal("Clean deleted before validating all keys")
	}
	p.page = &store.Page{Next: "issued"}
	if _, err := n.List(t.Context(), "a/", "", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := n.List(t.Context(), "b/", "issued", 2); !errors.Is(err, errNamespace) {
		t.Fatal("List accepted another prefix's token")
	}
	if _, err := n.List(t.Context(), "a/", "issued", 2); !errors.Is(err, errNamespace) {
		t.Fatal("List accepted repeated token")
	}
	p.fail = errors.New("endpoint credential sentinel")
	if _, err := n.List(t.Context(), "", "", 2); err == nil || strings.Contains(err.Error(), "sentinel") || !errors.Is(err, p.fail) {
		t.Fatal("SDK error was not withheld or lost error identity")
	}
}

func TestNamespaceRequiresStoreAndValidPageLimit(t *testing.T) {
	for _, limit := range []int{0, -1, 1001} {
		if _, err := newNamespace(store.NewLocal(t.TempDir()), limit); !errors.Is(err, errNamespace) {
			t.Fatal("newNamespace accepted invalid limit")
		}
	}
	if _, err := newNamespace(nil, 2); !errors.Is(err, errNamespace) {
		t.Fatal("newNamespace accepted nil store")
	}
	a, _ := testNamespace(t)
	b, _ := testNamespace(t)
	if a.prefix == b.prefix {
		t.Fatal("newNamespace reused nonce")
	}
}

func TestNamespaceCleanupContinuesAfterDeleteFailureAndWithholdsError(t *testing.T) {
	n, p := testNamespace(t)
	for _, key := range []string{"one", "two"} {
		if err := n.Put(t.Context(), key, []byte("synthetic"), nil); err != nil {
			t.Fatal(err)
		}
	}
	p.deleteFail = n.prefix + "one"
	count, err := n.clean(t.Context())
	if count != 1 || err == nil || strings.Contains(err.Error(), "credential sentinel") {
		t.Fatal("Clean did not report a sanitised partial cleanup failure")
	}
	if _, err := n.Head(t.Context(), "two"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("Clean stopped before deleting another owned object")
	}
	p.deleteFail = ""
	if count, err := n.clean(t.Context()); err != nil || count != 1 {
		t.Fatalf("retry Clean = %d, %v, want 1", count, err)
	}
}

func TestNamespaceDeferredCleanupSurvivesCancelledWork(t *testing.T) {
	bucket := store.NewLocal(t.TempDir())
	if err := bucket.Put(t.Context(), "v3/store.json", []byte("existing archive"), nil); err != nil {
		t.Fatal(err)
	}
	cleaned := false
	t.Run("cancelled", func(t *testing.T) {
		n, err := newNamespace(bucket, 2)
		if err != nil {
			t.Fatal(err)
		}
		// Register before mutation, as the live harness does after empty preflight.
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
			defer cancel()
			if count, err := n.clean(ctx); err != nil || count != 2 {
				t.Fatalf("deferred Clean = %d, %v, want 2", count, err)
			}
			cleaned = true
		})
		for _, key := range []string{"one", "two"} {
			if err := n.Put(t.Context(), key, []byte("synthetic"), nil); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := n.keys(ctx, ""); !errors.Is(err, context.Canceled) {
			t.Fatal("Keys ignored cancelled work context")
		}
	})
	if !cleaned {
		t.Fatal("deferred cleanup did not run")
	}
	body, err := bucket.Get(t.Context(), "v3/store.json")
	if err != nil || string(body) != "existing archive" {
		t.Fatal("deferred cleanup changed pre-existing archive")
	}
}
