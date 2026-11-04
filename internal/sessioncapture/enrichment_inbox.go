package sessioncapture

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gofrs/flock"
)

// This independent explicit budget is not part of encoded payload quota.
const (
	MaxNativeEvidenceBytes   = 64 << 10
	MaxNativeEvidenceRecords = 1024
)

func lockNativeInbox(ctx context.Context, spool string) (string, *flock.Flock, error) {
	return lockNamedInbox(ctx, spool, "native-evidence")
}

func nativeEntries(dir string) ([]os.DirEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(MaxNativeEvidenceRecords + 1)
	closeErr := f.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > MaxNativeEvidenceRecords {
		return nil, ErrQuota
	}
	return entries, nil
}

func WriteNativeEvidence(ctx context.Context, spool string, record NativeEvidence) error {
	body, err := validateNative(record)
	if err != nil {
		return err
	}
	dir, lock, err := lockNativeInbox(ctx, spool)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	path := filepath.Join(dir, digest([]byte(record.ID))+".json")
	if _, err := os.Lstat(path); err == nil {
		if err := privateInboxPath(path, false); err != nil {
			return err
		}
		old, err := readNativeFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(old, body) {
			return ErrIntegrity
		}
		return syncDir(dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := nativeEntries(dir)
	if err != nil {
		return err
	}
	if len(entries) >= MaxNativeEvidenceRecords {
		return ErrQuota
	}
	tmp, err := os.CreateTemp(dir, ".native-")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func readNativeFile(path string) ([]byte, error) {
	if err := privateInboxPath(path, false); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, MaxNativeEvidenceBytes+1))
	if len(b) > MaxNativeEvidenceBytes {
		return nil, ErrQuota
	}
	return b, err
}

func (q *Queue) drainNativeEvidence(ctx context.Context) error {
	dir, lock, err := lockNativeInbox(ctx, q.dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	entries, err := nativeEntries(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })
	runtime := q.runtimeLocal()
	runtime.mu.Lock()
	start := runtime.nativeCursor % len(entries)
	runtime.nativeCursor = (start + 32) % len(entries)
	runtime.mu.Unlock()
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	q.quota.keepOnly(names)
	for n := range min(len(entries), 32) {
		if err := q.drainNativeEntry(ctx, dir, entries[(start+n)%len(entries)].Name()); err != nil {
			return err
		}
	}
	return nil
}

// drainNativeEntry adopts one inbox file and removes it only after durable
// adoption. A quota rejection keeps the file and backs the entry off, so an
// exhausted capacity is not re-probed (policy callback, Lstat, COUNT scans)
// every tick. Other per-file failures keep their prior behaviour: skip.
func (q *Queue) drainNativeEntry(ctx context.Context, dir, name string) error {
	if q.quota.blocked(name, q.opts.Now()) {
		return nil
	}
	path := filepath.Join(dir, name)
	record, ok := readNativeRecord(path)
	if !ok {
		return nil
	}
	if err := q.AcceptNativeEvidence(ctx, record); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrQuota) {
			q.quota.fail(name, q.opts.Now())
		}
		return nil
	}
	q.quota.clear(name)
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDir(dir)
}

// readNativeRecord reports ok=false for an unreadable or undecodable file,
// which the drain leaves in place exactly as before.
func readNativeRecord(path string) (NativeEvidence, bool) {
	var record NativeEvidence
	body, err := readNativeFile(path)
	if err != nil {
		return record, false
	}
	return record, json.Unmarshal(body, &record) == nil
}

func (q *Queue) nativeInboxLoop(ctx context.Context) error {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for ctx.Err() == nil {
		if err := q.drainNativeEvidence(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
	return nil
}
