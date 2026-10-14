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
	"github.com/mich-murphy/memory/internal/raw"
)

type Store interface {
	Put(ctx context.Context, key string, body []byte, meta map[string]string) error
	PutFile(ctx context.Context, key, path string, meta map[string]string) error
	// Copy copies src to dst only when src holds the bytes sha names, and leaves an existing dst
	// alone. Copies go to content-addressed keys, and src may hold bytes that replaced the ones
	// recorded for it in an upload whose answer was lost. ok says whether dst holds them now.
	Copy(ctx context.Context, src, dst, sha string) (ok bool, err error)
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

// Copy reads sha from src's bytes, since a local directory keeps no metadata.
func (l *Local) Copy(ctx context.Context, src, dst, sha string) (ok bool, err error) {
	if fsutil.IsFile(l.path(dst)) {
		return true, nil
	}
	body, err := os.ReadFile(l.path(src))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("copy %s: %w", src, err)
	}
	held := ""
	if data, err := raw.Decompress(body); err == nil {
		held = raw.SHA256(data)
	}
	if held != sha {
		return false, nil
	}
	return true, l.Put(ctx, dst, body, nil)
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
