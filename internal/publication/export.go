package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ExportSession pins one manifest before creating any bodies. dest must not exist
// and its parent must be private/trusted. metadata.json is the completion marker;
// failures may leave a private partial directory without that marker.
func ExportSession(ctx context.Context, b Backend, i Identity, dest string) (Export, error) {
	var out Export
	m, err := pinned(ctx, b, i)
	if err != nil {
		return out, err
	}
	if err := noSymlinks(dest); err != nil {
		return out, err
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		return out, err
	}
	out = Export{Contract: ReaderContract, Manifest: m, Bodies: make([]Body, 0, len(m.Sources))}
	r, err := os.OpenRoot(dest)
	if err != nil {
		return out, err
	}
	defer func() { _ = r.Close() }() // Root descriptor close does not flush object writes.
	if err := mkdirWithin(r, "bodies"); err != nil {
		return out, err
	}
	for n := range m.Sources {
		s := &m.Sources[n]
		if !IsSelected(*s) {
			continue
		}
		name := fmt.Sprintf("bodies/%04d.bin", n)
		f, e := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if e != nil {
			return out, e
		}
		e = streamObject(ctx, b, s.Object, f)
		e = errors.Join(e, f.Sync(), f.Close())
		if e != nil {
			return out, e
		}
		out.Bodies = append(out.Bodies, Body{SourceID: s.SourceID, EpochID: s.EpochID, Path: name, SHA256: s.Object.SHA256, Size: s.Object.Size})
	}
	if err := syncDir(r, "bodies"); err != nil {
		return out, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if len(data) > MaxMetadata {
		return out, errors.New("export metadata exceeds limit")
	}
	f, err := r.OpenFile(".metadata.tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return out, err
	}
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return out, err
	}
	if err := r.Rename(".metadata.tmp", "metadata.json"); err != nil {
		return out, err
	}
	if err := syncDir(r, "."); err != nil {
		return out, err
	}
	parent, err := os.Open(filepath.Dir(dest))
	if err != nil {
		return out, err
	}
	return out, errors.Join(parent.Sync(), parent.Close())
}
