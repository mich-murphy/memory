package publication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// Local keeps lock/control files outside the data object namespace. The root and
// its ancestors must be owned/trusted; untrusted concurrent filesystem mutation
// is not a supported operating mode. os.Root also confines symlink resolution.
type Local struct {
	root string
	// syncFile is an optional test seam; production uses os.File.Sync.
	syncFile func(*os.File) error
}

func (l *Local) sync(f *os.File) error {
	var err error
	if l.syncFile != nil {
		err = l.syncFile(f)
	} else {
		err = f.Sync()
	}
	if err != nil {
		return errors.Join(ErrDurability, err)
	}
	return nil
}

func NewLocal(root string) *Local { return &Local{root: root} }

func validKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, "\\\x00") && !strings.HasPrefix(key, "/") && path.Clean(key) == key && key != "." && key != ".." && !strings.HasPrefix(key, "../")
}

// The configured root itself must not be a symlink. Ancestors are trusted and
// may include platform aliases such as Darwin's /var -> /private/var.
func noSymlinks(p string) error {
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlink path refused")
	}
	return nil
}

func durableSync(f *os.File) error {
	if err := f.Sync(); err != nil {
		return errors.Join(ErrDurability, err)
	}
	return nil
}

func syncDir(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(durableSync(f), f.Close())
}

func (l *Local) openRoot() (*os.Root, error) {
	if err := noSymlinks(l.root); err != nil {
		return nil, err
	}
	// Create ancestors one at a time and sync the parent of every new directory.
	abs, err := filepath.Abs(l.root)
	if err != nil {
		return nil, err
	}
	if err := mkdirDurable(abs); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	if err = mkdirWithin(r, "data"); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func mkdirDurable(p string) error {
	st, err := os.Lstat(p)
	if err == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := mkdirDurable(filepath.Dir(p)); err != nil {
		return err
	}
	if err = os.Mkdir(p, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	f, err := os.Open(filepath.Dir(p))
	if err != nil {
		return err
	}
	return errors.Join(durableSync(f), f.Close())
}

func mkdirWithin(r *os.Root, dir string) error {
	cur := ""
	for part := range strings.SplitSeq(dir, "/") {
		cur = path.Join(cur, part)
		st, err := r.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			if err = r.Mkdir(cur, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			if err := syncDir(r, path.Dir(cur)); err != nil {
				return err
			}
			st, err = r.Lstat(cur)
		}
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe object directory")
		}
	}
	return nil
}

// localObjectName changes only physical storage, never the logical wire key.
// Keep ordinary fixtures at data/<key>. Long names use a disjoint root, so even
// a logical key named long-data/<digest> remains distinct at data/long-data/...
// SHA-256 has the same collision-resistance assumption as object content IDs.
func localObjectName(key string) string {
	if len(key) > 768 {
		return "long-data/" + token([]byte(key))
	}
	for part := range strings.SplitSeq(key, "/") {
		if len(part) > 240 {
			return "long-data/" + token([]byte(key))
		}
	}
	return "data/" + key
}

// Validate the logical key before mapping, then reject symlinks in every
// physical component (including the selected storage root) under os.Root.
func safeObject(r *os.Root, key string) (string, error) {
	if !validKey(key) {
		return "", errors.New("invalid object key")
	}
	name := localObjectName(key)
	cur := ""
	for part := range strings.SplitSeq(name, "/") {
		cur = path.Join(cur, part)
		st, err := r.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlink object refused")
		}
	}
	return name, nil
}

func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := l.openRoot()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }() // Root descriptor close does not flush object writes.
	name, err := safeObject(r, key)
	if err != nil {
		return nil, err
	}
	f, err := r.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("non-regular object")
	}
	return f, nil
}

func (l *Local) locked(ctx context.Context, fn func(*os.Root) error) (retErr error) {
	r, err := l.openRoot()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }() // Root descriptor close does not flush object writes.
	lockPath := filepath.Join(l.root, ".cas.lock")
	if err := noSymlinks(lockPath); err != nil {
		return err
	}
	lock := flock.New(lockPath, flock.SetPermissions(0o600))
	ok, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return ctx.Err()
	}
	defer func() { retErr = errors.Join(retErr, lock.Close()) }()
	return fn(r)
}
func token(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (l *Local) Current(ctx context.Context, key string) ([]byte, string, error) {
	f, err := l.Open(ctx, key)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }() // Read-only descriptor.
	b, err := io.ReadAll(io.LimitReader(f, MaxMetadata+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > MaxMetadata {
		return nil, "", errors.New("metadata exceeds limit")
	}
	return b, token(b), nil
}

func (l *Local) atomicWrite(r *os.Root, key string, src io.Reader) error {
	dst, err := safeObject(r, key)
	if err != nil {
		return err
	}
	dir := path.Dir(dst)
	if err := mkdirWithin(r, dir); err != nil {
		return err
	}
	// Exclusive creation makes temporary names safe even across processes.
	var f *os.File
	var tmp string
	for n := range 100 {
		tmp = path.Join(dir, fmt.Sprintf(".tmp-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), n))
		var err error
		f, err = r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	if f == nil {
		return errors.New("cannot allocate temporary object")
	}
	defer func() { _ = r.Remove(tmp) }() // Best-effort unselected temporary cleanup.
	_, err = io.Copy(f, src)
	err = errors.Join(err, l.sync(f), f.Close())
	if err != nil {
		return err
	}
	if err := r.Rename(tmp, dst); err != nil {
		return err
	}
	return l.syncObject(r, key)
}

func (l *Local) CompareAndSwap(ctx context.Context, key, expected string, b []byte) error {
	if len(b) > MaxMetadata {
		return errors.New("metadata exceeds limit")
	}
	return l.locked(ctx, func(r *os.Root) error {
		_, got, err := l.Current(ctx, key)
		if errors.Is(err, ErrNotFound) {
			got = ""
		} else if err != nil {
			return err
		}
		if got != expected {
			return ErrConflict
		}
		return l.atomicWrite(r, key, strings.NewReader(string(b)))
	})
}

func (l *Local) PutFile(ctx context.Context, key, filename string) error {
	return l.locked(ctx, func(r *os.Root) error {
		if _, err := safeObject(r, key); err != nil {
			return err
		}
		src, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }() // Read-only upload source.
		st, err := src.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return errors.New("non-regular upload")
		}
		old, err := l.Open(ctx, key)
		if err == nil {
			defer func() { _ = old.Close() }() // Read-only existing object.
			if err := compareReaders(ctx, src, old); err != nil {
				return err
			}
			return l.syncObject(r, key)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		return l.atomicWrite(r, key, &contextReader{ctx: ctx, r: src})
	})
}

// Sync fences even already-visible objects under the writer lock. Reopening the
// store after a failed sync must not turn a visible but unsynced rename into ack.
func (l *Local) Sync(ctx context.Context, key string) error {
	return l.locked(ctx, func(r *os.Root) error { return l.syncObject(r, key) })
}

func (l *Local) syncObject(r *os.Root, key string) error {
	name, err := safeObject(r, key)
	if err != nil {
		return err
	}
	f, err := r.Open(name)
	if err != nil {
		return errors.Join(ErrDurability, err)
	}
	if err := errors.Join(l.sync(f), f.Close()); err != nil {
		return errors.Join(ErrDurability, err)
	}
	// Include every ancestor: an earlier failed mkdir sync may have left any
	// existing parent visible but not durable, even across process restart.
	for dir := path.Dir(name); ; dir = path.Dir(dir) {
		f, err := r.Open(dir)
		if err != nil {
			return errors.Join(ErrDurability, err)
		}
		if err := errors.Join(l.sync(f), f.Close()); err != nil {
			return errors.Join(ErrDurability, err)
		}
		if dir == "." {
			break
		}
	}
	abs, err := filepath.Abs(l.root)
	if err != nil {
		return errors.Join(ErrDurability, err)
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		f, err := os.Open(dir)
		if err != nil {
			return errors.Join(ErrDurability, err)
		}
		if err := errors.Join(l.sync(f), f.Close()); err != nil {
			return errors.Join(ErrDurability, err)
		}
		if dir == filepath.Dir(dir) {
			return nil
		}
	}
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *contextReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(b)
}

func compareReaders(ctx context.Context, src, old io.Reader) error {
	a, b := sha256.New(), sha256.New()
	if _, err := io.Copy(a, &contextReader{ctx: ctx, r: src}); err != nil {
		return err
	}
	if _, err := io.Copy(b, &contextReader{ctx: ctx, r: old}); err != nil {
		return err
	}
	if !bytes.Equal(a.Sum(nil), b.Sum(nil)) {
		return ErrConflict
	}
	return nil
}
