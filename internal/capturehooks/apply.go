package capturehooks

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mich-murphy/memory/internal/capturedaemon"
)

// Apply is the sole mutation boundary. A false apply is always a no-op. Plans
// must originate in this process from Build, not decoded JSON or exported facts.
// Each target is independently durable; a failed multi-target apply may be partial.
func Apply(ctx context.Context, p Plan, apply bool) error {
	if !apply {
		return nil
	}
	if !p.valid {
		return errors.New("hook plan must be built before apply")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateOptions(p.options); err != nil {
		return err
	}
	// Preflight every entry before changing any entry, including unchanged targets.
	for _, e := range p.entries {
		if err := precondition(e); err != nil {
			return err
		}
	}
	for _, e := range p.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.remove && bytes.Equal(e.before, e.after) {
			continue
		}
		if err := applyEntry(e); err != nil {
			return errors.New("hook apply failed; inspect target hashes before replanning")
		}
	}
	return nil
}

func precondition(e entry) error {
	b, exists, err := readTarget(e.path)
	if err != nil {
		return err
	}
	if exists != e.exists || digest(b) != digest(e.before) || !bytes.Equal(b, e.before) {
		return errors.New("hook settings changed since plan; replan required")
	}
	return nil
}

func applyEntry(e entry) error {
	if err := precondition(e); err != nil {
		return err
	}
	dir := filepath.Dir(e.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if e.exists {
		if err := backup(e); err != nil {
			return err
		}
	}
	// Recheck after backup creation and immediately before replacing/removing.
	if err := precondition(e); err != nil {
		return err
	}
	if e.remove {
		if err := os.Remove(e.path); err != nil {
			return err
		}
		return syncParents(dir)
	}
	// Existing native settings can be public-readable; replace with a private temp
	// rather than rejecting them (capturedaemon.WritePrivate correctly rejects it).
	f, err := os.CreateTemp(dir, ".capture-v1-write-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err := writeSyncClose(f, e.after); err != nil {
		return err
	}
	if err := precondition(e); err != nil {
		return err
	}
	if err := capturedaemon.SafePath(e.path); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), e.path); err != nil {
		return err
	}
	return syncParents(dir)
}

// Use the existing capturepolicy backup category: private permissions alone do
// not prevent discovery from treating an unrecognized settings copy as raw.
func backup(e entry) error {
	f, err := os.CreateTemp(filepath.Dir(e.path), filepath.Base(e.path)+".agent-sessions-backup-")
	if err != nil {
		return err
	}
	// Preserve private backups even on a later failed apply; never overwrite one.
	if err := writeSyncClose(f, e.before); err != nil {
		return err
	}
	return syncParents(filepath.Dir(e.path))
}

func writeSyncClose(f *os.File, b []byte) error {
	_, err := f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func syncParents(dir string) error {
	for {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		if err := errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
