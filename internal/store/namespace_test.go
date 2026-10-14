package store_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/store"
)

// namespace is a test-boundary adapter, not a production archive prefix option.
// Even v3/store.json and shared blobs are translated into a fresh private namespace.
// The underlying store is deliberately private: cleanup cannot bypass confinement.
type namespace struct {
	bucket store.Store
	prefix string
	limit  int
	mu     sync.Mutex
	tokens map[string]string
	pages  int
}

var errNamespace = errors.New("test namespace: invalid or unconfined key, prefix, token or response")

// newNamespace creates a cryptographically random, owned test namespace. limit also
// caps the engine's LIST requests, so small values exercise real service pagination.
func newNamespace(bucket store.Store, limit int) (*namespace, error) {
	if bucket == nil || limit < 1 || limit > 1000 {
		return nil, errNamespace
	}
	return &namespace{bucket: bucket, prefix: "_agent-sessions/test/" + rand.Text() + "/", limit: limit, tokens: map[string]string{}}, nil
}

// continuationPages reports replies with a continuation token, without revealing opaque tokens.
func (n *namespace) continuationPages() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.pages
}

func (n *namespace) key(key string) (string, error) {
	if !raw.ValidKey(key) || strings.HasPrefix(key, "_agent-sessions/") {
		return "", errNamespace
	}
	return n.prefix + key, nil
}

func (n *namespace) listPrefix(prefix string) (string, error) {
	if prefix == "" {
		return n.prefix, nil
	}
	if _, err := n.key(strings.TrimSuffix(prefix, "/")); err != nil {
		return "", err
	}
	return n.prefix + prefix, nil
}

// namespaceError keeps raw SDK errors (which may include endpoint or credentials)
// out of test and Syncer logs, while preserving errors.Is/As for protocol checks.
type namespaceError struct{ cause error }

func (e namespaceError) Error() string {
	return "test namespace: store operation failed (details withheld)"
}
func (e namespaceError) Unwrap() error { return e.cause }

func namespaceResult(err error) error {
	if err == nil {
		return nil
	}
	return namespaceError{cause: err}
}

func (n *namespace) List(ctx context.Context, prefix, token string, limit int) (store.Page, error) {
	physical, err := n.listPrefix(prefix)
	if err != nil || limit < 1 || limit > 1000 {
		return store.Page{}, errNamespace
	}
	n.mu.Lock()
	known, ok := n.tokens[token]
	n.mu.Unlock()
	if token != "" && (!ok || known != prefix) {
		return store.Page{}, errNamespace
	}
	limit = min(limit, n.limit)
	page, err := n.bucket.List(ctx, physical, token, limit)
	if err != nil {
		return store.Page{}, namespaceResult(err)
	}
	if len(page.Objects) > limit || page.Next != "" && page.Next == token {
		return store.Page{}, errNamespace
	}
	// Validate the entire response before stripping even one key.
	for _, obj := range page.Objects {
		if !raw.ValidKey(obj.Key) || !strings.HasPrefix(obj.Key, physical) || !strings.HasPrefix(obj.Key, n.prefix) {
			return store.Page{}, errNamespace
		}
		if _, err := n.key(strings.TrimPrefix(obj.Key, n.prefix)); err != nil {
			return store.Page{}, errNamespace
		}
	}
	for i := range page.Objects {
		page.Objects[i].Key = strings.TrimPrefix(page.Objects[i].Key, n.prefix)
	}
	if page.Next != "" {
		n.mu.Lock()
		n.tokens[page.Next] = prefix
		n.pages++
		n.mu.Unlock()
	}
	return page, nil
}

func (n *namespace) Head(ctx context.Context, key string) (store.Object, error) {
	physical, err := n.key(key)
	if err != nil {
		return store.Object{}, err
	}
	obj, err := n.bucket.Head(ctx, physical)
	if err != nil {
		return store.Object{}, namespaceResult(err)
	}
	if obj.Key != physical {
		return store.Object{}, errNamespace
	}
	obj.Key = key
	return obj, nil
}

func (n *namespace) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	physical, err := n.key(key)
	if err != nil {
		return nil, err
	}
	reader, err := n.bucket.OpenReader(ctx, physical)
	return reader, namespaceResult(err)
}

func (n *namespace) Create(ctx context.Context, key string, body []byte) error {
	physical, err := n.key(key)
	if err != nil {
		return err
	}
	return namespaceResult(n.bucket.Create(ctx, physical, body))
}

func (n *namespace) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	physical, err := n.key(key)
	if err != nil {
		return err
	}
	return namespaceResult(n.bucket.Put(ctx, physical, body, meta))
}

func (n *namespace) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	physical, err := n.key(key)
	if err != nil {
		return err
	}
	return namespaceResult(n.bucket.PutFile(ctx, physical, path, meta))
}

func (n *namespace) Get(ctx context.Context, key string) ([]byte, error) {
	physical, err := n.key(key)
	if err != nil {
		return nil, err
	}
	body, err := n.bucket.Get(ctx, physical)
	return body, namespaceResult(err)
}

func (n *namespace) Copy(ctx context.Context, src, dst, sha string) (bool, error) {
	physicalSrc, err := n.key(src)
	if err != nil {
		return false, err
	}
	physicalDst, err := n.key(dst)
	if err != nil {
		return false, err
	}
	ok, err := n.bucket.Copy(ctx, physicalSrc, physicalDst, sha)
	return ok, namespaceResult(err)
}

func (n *namespace) Delete(ctx context.Context, key string) error {
	physical, err := n.key(key)
	if err != nil {
		return err
	}
	return namespaceResult(n.bucket.Delete(ctx, physical))
}

// keys enumerates only this owned prefix. A finite page bound also fails closed
// on a service that cycles opaque tokens. No broad bucket listing is possible.
func (n *namespace) keys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	token := ""
	seen := map[string]bool{}
	for range 10000 {
		if err := ctx.Err(); err != nil {
			return nil, namespaceResult(err)
		}
		page, err := n.List(ctx, prefix, token, 1000)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Objects {
			if seen[obj.Key] {
				return nil, errNamespace
			}
			seen[obj.Key] = true
			keys = append(keys, obj.Key)
		}
		if page.Next == "" {
			return keys, nil
		}
		token = page.Next
	}
	return nil, errNamespace
}

// clean validates all listed keys before any deletion, deletes only owned objects,
// then verifies the prefix is empty. Callers supply a fresh bounded cleanup context.
func (n *namespace) clean(ctx context.Context) (int, error) {
	keys, err := n.keys(ctx, "")
	if err != nil {
		return 0, err
	}
	var failures error
	deleted := 0
	for _, key := range keys {
		if err := n.Delete(ctx, key); err != nil {
			failures = errors.Join(failures, err)
		} else {
			deleted++
		}
	}
	page, err := n.List(ctx, "", "", 1)
	if err != nil {
		failures = errors.Join(failures, err)
	} else if len(page.Objects) != 0 || page.Next != "" {
		failures = errors.Join(failures, errors.New("test namespace: cleanup left objects"))
	}
	return deleted, failures
}
