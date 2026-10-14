// Package store holds the object stores: R2 (S3 API) and a local directory with the same key
// layout.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
)

type Store interface {
	Put(ctx context.Context, key string, body []byte, meta map[string]string) error
	PutFile(ctx context.Context, key, path string, meta map[string]string) error
	// Copy leaves an existing dst alone. Copies go to content-addressed keys, and a retry after
	// an upload whose answer was lost would otherwise copy bytes that have since replaced src.
	Copy(ctx context.Context, src, dst string) (srcExists bool, err error)
	Delete(ctx context.Context, key string) error
	Get(ctx context.Context, key string) ([]byte, error)
}

func Open(cfg *config.Config) (Store, error) {
	if cfg.LocalStore != "" {
		return NewLocal(cfg.LocalStore), nil
	}
	return NewR2(cfg)
}

type Local struct {
	root string
}

func NewLocal(dir string) *Local {
	return &Local{root: dir}
}

func (l *Local) path(key string) string {
	return filepath.Join(l.root, filepath.FromSlash(key))
}

func (l *Local) Put(_ context.Context, key string, body []byte, _ map[string]string) error {
	return fsutil.WriteFileAtomic(l.path(key), body, 0o600)
}

func (l *Local) PutFile(_ context.Context, key, path string, _ map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	defer func() { _ = f.Close() }() // read-only
	return fsutil.WriteAtomic(l.path(key), f, 0o600)
}

func (l *Local) Copy(ctx context.Context, src, dst string) (srcExists bool, err error) {
	if fsutil.IsFile(l.path(dst)) {
		return true, nil
	}
	if !fsutil.IsFile(l.path(src)) {
		return false, nil
	}
	return true, l.PutFile(ctx, dst, l.path(src), nil)
}

func (l *Local) Delete(_ context.Context, key string) error {
	if err := os.Remove(l.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (l *Local) Get(_ context.Context, key string) ([]byte, error) {
	data, err := os.ReadFile(l.path(key))
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return data, nil
}
