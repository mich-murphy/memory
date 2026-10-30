package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultExportMaxBytes int64 = 1 << 30
	MaxExportBytes        int64 = 1 << 40
)

// ErrExportBudget is content-free and reports invalid or exceeded export budgets.
var ErrExportBudget = errors.New("publication: export byte budget invalid or exceeded")

// ExportSession exports with the default 1-GiB total decoded-body budget.
func ExportSession(ctx context.Context, b Backend, i Identity, dest string) (Export, error) {
	return ExportSessionBounded(ctx, b, i, dest, DefaultExportMaxBytes)
}

// ExportSessionBounded pins one manifest before creating any bodies. dest must not exist
// and its parent must be private/trusted. metadata.json is the completion marker;
// failures may leave a private partial directory without that marker.
func ExportSessionBounded(ctx context.Context, b Backend, i Identity, dest string, maxBytes int64) (Export, error) {
	var out Export
	if maxBytes <= 0 || maxBytes > MaxExportBytes {
		return out, ErrExportBudget
	}
	m, err := pinned(ctx, b, i)
	if err != nil {
		return out, err
	}
	if err := checkExportBudget(m, maxBytes); err != nil {
		return out, err
	}
	return exportPinned(ctx, b, m, dest)
}

func checkExportBudget(m Manifest, maxBytes int64) error {
	// Subtraction avoids aggregate overflow, including hostile declared sizes.
	remaining := maxBytes
	for n := range m.Sources {
		source := &m.Sources[n]
		if !IsSelected(*source) {
			continue
		}
		if source.Object.Size > remaining {
			return ErrExportBudget
		}
		remaining -= source.Object.Size
	}
	return nil
}

// exportMetadata is the shared prospective consumer envelope. Body coordinates
// depend only on the pinned manifest, never on body I/O or destination state.
func exportMetadata(m Manifest) (Export, []byte, error) {
	out := Export{Contract: ReaderContract, Manifest: m, Bodies: make([]Body, 0, len(m.Sources))}
	for n := range m.Sources {
		s := &m.Sources[n]
		if IsSelected(*s) {
			out.Bodies = append(out.Bodies, Body{SourceID: s.SourceID, EpochID: s.EpochID, Path: fmt.Sprintf("bodies/%04d.bin", n), SHA256: s.Object.SHA256, Size: s.Object.Size})
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return out, nil, err
	}
	if len(data) > MaxMetadata {
		return out, nil, errors.New("export metadata exceeds limit")
	}
	return out, data, nil
}

func exportPinned(ctx context.Context, b Backend, m Manifest, dest string) (Export, error) {
	out, data, err := exportMetadata(m)
	if err != nil {
		return out, err
	}
	if err := noSymlinks(dest); err != nil {
		return out, err
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		return out, err
	}
	r, err := os.OpenRoot(dest)
	if err != nil {
		return out, err
	}
	defer func() { _ = r.Close() }() // Root descriptor close does not flush object writes.
	if err := mkdirWithin(r, "bodies"); err != nil {
		return out, err
	}
	selected := 0
	for n := range m.Sources {
		s := &m.Sources[n]
		if !IsSelected(*s) {
			continue
		}
		name := out.Bodies[selected].Path
		selected++
		f, e := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if e != nil {
			return out, e
		}
		e = streamObject(ctx, b, s.Object, f)
		e = errors.Join(e, f.Sync(), f.Close())
		if e != nil {
			return out, e
		}
	}
	if err := syncDir(r, "bodies"); err != nil {
		return out, err
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
