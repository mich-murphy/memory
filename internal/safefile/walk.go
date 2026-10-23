package safefile

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const walkBatch = 128

// Walk enumerates in batches, never sorting or buffering a whole directory. The
// entry/depth budgets include the root. At most depth+1 directory FDs are live.
// Complete consumers sort their bounded final selections, not unbounded inputs.
func Walk(ctx context.Context, root string, limit, depth int, visit fs.WalkDirFunc) error {
	return walk(ctx, root, limit, depth, visit, func(f *os.File, n int) ([]fs.DirEntry, error) { return f.ReadDir(n) })
}

type enumerator func(*os.File, int) ([]fs.DirEntry, error)

type walker struct {
	ctx              context.Context
	remaining, depth int
	visit            fs.WalkDirFunc
	root, canonical  string
	read             enumerator
}

func walk(ctx context.Context, root string, limit, depth int, visit fs.WalkDirFunc, read enumerator) error {
	st, err := os.Lstat(root)
	if err != nil {
		return visit(root, nil, err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return err
	}
	w := walker{root: root, canonical: canonical, ctx: ctx, remaining: limit, depth: depth, visit: visit, read: read}
	return w.entry(root, fs.FileInfoToDirEntry(st), 0)
}

func (w *walker) entry(path string, d fs.DirEntry, depth int) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.remaining <= 0 {
		return errors.New("safefile: entry capacity exceeded; traversal incomplete")
	}
	w.remaining--
	err := w.visit(path, d, nil)
	if errors.Is(err, filepath.SkipDir) && d.IsDir() {
		return nil
	}
	if err != nil || !d.IsDir() {
		return err
	}
	if depth >= w.depth {
		return errors.New("safefile: depth capacity exceeded; traversal incomplete")
	}
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return err
	}
	f, err := OpenDirectory(w.ctx, filepath.Join(w.canonical, rel))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read-only directory
	return w.directory(f, path, depth)
}

func (w *walker) directory(f *os.File, path string, depth int) error {
	for {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		if w.remaining <= 0 {
			return errors.New("safefile: entry capacity exceeded; traversal incomplete")
		}
		entries, err := w.read(f, min(walkBatch, w.remaining))
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, d := range entries {
			if err := w.entry(filepath.Join(path, d.Name()), d, depth+1); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
