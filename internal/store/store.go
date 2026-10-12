// Package store holds the object stores: R2 (S3 API) and a local directory with the same key
// layout.
package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
)

// Store is a flat key/value object store.
type Store interface {
	Put(ctx context.Context, key string, body []byte, meta map[string]string) error
	// PutFile uploads a file from disk, streaming large ones.
	PutFile(ctx context.Context, key, path string, meta map[string]string) error
	// Copy is a server-side copy; false if src doesn't exist.
	Copy(ctx context.Context, src, dst string) (bool, error)
	Exists(ctx context.Context, key string) (bool, error)
	Delete(ctx context.Context, key string) error
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
}

// Open returns the store the config names: a local directory if set, otherwise R2.
func Open(cfg *config.Config) (Store, error) {
	if cfg.LocalStore != "" {
		return NewLocal(cfg.LocalStore), nil
	}
	return NewR2(cfg)
}

// Local stores objects as files under a root directory.
type Local struct {
	root string
}

// NewLocal returns a store rooted at dir.
func NewLocal(dir string) *Local {
	return &Local{root: dir}
}

func (l *Local) path(key string) string {
	return filepath.Join(l.root, filepath.FromSlash(key))
}

// Put writes body under key.
func (l *Local) Put(_ context.Context, key string, body []byte, _ map[string]string) error {
	return fsutil.WriteFileAtomic(l.path(key), body, 0o600)
}

// PutFile copies the file at path under key.
func (l *Local) PutFile(_ context.Context, key, path string, _ map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	defer func() { _ = f.Close() }() // read-only
	return fsutil.WriteAtomic(l.path(key), f, 0o600)
}

// Copy copies src to dst.
func (l *Local) Copy(ctx context.Context, src, dst string) (bool, error) {
	if !fsutil.IsFile(l.path(src)) {
		return false, nil
	}
	return true, l.PutFile(ctx, dst, l.path(src), nil)
}

// Exists reports whether key is stored.
func (l *Local) Exists(_ context.Context, key string) (bool, error) {
	return fsutil.Exists(l.path(key)), nil
}

// Delete removes key; a missing key is not an error.
func (l *Local) Delete(_ context.Context, key string) error {
	if err := os.Remove(l.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

// Get reads key.
func (l *Local) Get(_ context.Context, key string) ([]byte, error) {
	data, err := os.ReadFile(l.path(key))
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return data, nil
}

// List returns the keys starting with prefix, sorted.
func (l *Local) List(_ context.Context, prefix string) ([]string, error) {
	base := l.root
	if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
		base = l.path(prefix[:i])
	}
	if !fsutil.IsDir(base) {
		return nil, nil
	}
	var keys []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.Contains(d.Name(), ".tmp-") {
			return nil
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		if key := filepath.ToSlash(rel); strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}
	slices.Sort(keys)
	return keys, nil
}

// readAllLimited reads r fully; used for object bodies.
func readAllLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return data, nil
}
