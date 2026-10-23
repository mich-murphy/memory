// Package safefile binds canonical paths to descriptors without following aliases.
package safefile

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// OpenResolved opens an already resolved absolute path. Every component is opened
// relative to its pinned parent with O_NOFOLLOW. expected must be observed before
// authorization; replacement, nonregular files and cancellation fail before reads.
// Resolve and authorize all alias identities before calling this function.
func OpenResolved(ctx context.Context, path string, expected fs.FileInfo) (*os.File, error) {
	f, err := open(ctx, path, false)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || expected == nil || !os.SameFile(st, expected)) {
		err = errors.New("safefile: replaced or nonregular input")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	} // read-only
	return f, nil
}

// OpenDirectory opens a canonical directory, rejecting aliases in all components.
func OpenDirectory(ctx context.Context, path string) (*os.File, error) { return open(ctx, path, true) }

func open(ctx context.Context, path string, directory bool) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("safefile: canonical absolute path required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "/" {
		parts = nil
	}
	for i, part := range parts {
		if err = ctx.Err(); err != nil {
			break
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		var next int
		next, err = unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd) // read-only parent
		fd = next
		if err != nil {
			return nil, err
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	} // read-only
	return os.NewFile(uintptr(fd), path), nil
}
