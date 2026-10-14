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

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/raw"
)

type Store interface {
	List(ctx context.Context, prefix, token string, limit int) (Page, error)
	Head(ctx context.Context, key string) (Object, error)
	OpenReader(ctx context.Context, key string) (io.ReadCloser, error)
	Create(ctx context.Context, key string, body []byte) error
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
	if err := l.validateKey(key); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(l.path(key), body, 0o600)
}

func (l *Local) PutFile(_ context.Context, key, path string, _ map[string]string) error {
	if err := l.validateKey(key); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	defer func() { _ = f.Close() }() // read-only
	return fsutil.WriteAtomic(l.path(key), f, 0o600)
}

// Copy reads sha from src's bytes, since a local directory keeps no metadata.
func (l *Local) Copy(ctx context.Context, src, dst, sha string) (ok bool, err error) {
	if err := l.validateKey(src); err != nil {
		return false, err
	}
	if err := l.validateKey(dst); err != nil {
		return false, err
	}
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
	if err := l.validateKey(key); err != nil {
		return err
	}
	if err := os.Remove(l.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (l *Local) Get(_ context.Context, key string) ([]byte, error) {
	if err := l.validateKey(key); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(l.path(key))
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return data, nil
}

// validateKey confines all local operations, including reads, to the resolved store root.
func (l *Local) validateKey(key string) error {
	if !raw.ValidKey(key) {
		return errors.New("store: invalid object key")
	}
	root, err := canonicalPath(l.root)
	if err != nil {
		return err
	}
	candidate, err := canonicalPath(l.path(key))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel != "." && !filepath.IsLocal(rel) {
		return errors.New("store: object key escapes root through symlink")
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var suffix []string
	for p := abs; ; p = filepath.Dir(p) {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			for _, part := range slices.Backward(suffix) {
				resolved = filepath.Join(resolved, part)
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if p == filepath.Dir(p) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(p))
	}
}
