package fsutil

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	return WriteAtomic(path, bytes.NewReader(data), perm)
}

func WriteAtomic(path string, r io.Reader, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = io.Copy(f, r); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Chmod(perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	// Not every filesystem can sync a directory; the rename has happened either way.
	_ = d.Sync()
	if err := d.Close(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}

func IsFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Within expects both paths absolute and clean.
func Within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}
