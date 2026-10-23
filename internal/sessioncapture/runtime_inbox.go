package sessioncapture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
)

var errInvalidHint = errors.New("sessioncapture: invalid inbox hint")

const (
	MaxHintBytes  = 64 << 10
	MaxInboxHints = 1024
)

// WriteHint is the hook/process boundary. It does not open the Queue or acquire
// queue.lock. Its short independent inbox lock never spans capture or network I/O.
// Return success means durable pathname observation only, not captured bytes.
func WriteHint(ctx context.Context, spool string, req Request) error {
	if err := validateHint(req); err != nil {
		return err
	}
	path, err := filepath.Abs(req.Path)
	if err != nil {
		return err
	}
	req.Path = path
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if len(body) > MaxHintBytes {
		return ErrQuota
	}
	dir, lock, err := lockInbox(ctx, spool)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	entries, err := boundedInboxEntries(dir)
	if err != nil {
		return err
	}
	// Coalesce observations, never accepted content. Distinct observations for one
	// source share a slot, avoiding an unbounded file per hook event.
	name := digest([]byte(req.Identity.BaseKey()+"\x00"+req.SourceID)) + ".json"
	if len(entries) >= MaxInboxHints {
		if _, err = os.Lstat(filepath.Join(dir, name)); err != nil {
			return ErrQuota
		}
	}
	tmp, err := os.CreateTemp(dir, ".hint-")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDir(dir)
}

func validateHint(req Request) error {
	if req.ObservedAtNS < 0 {
		return errors.New("sessioncapture: invalid hint timestamp")
	}
	q := Queue{opts: Options{ArchiveID: req.Identity.ArchiveID}}
	if req.Identity.ArchiveID == "" {
		return errors.New("sessioncapture: invalid hint archive")
	}
	if err := q.valid(req); err != nil {
		return err
	}
	for _, s := range []string{req.Identity.ArchiveID, req.Identity.MachineID, req.Identity.Agent, req.Identity.SessionID, req.SourceID, req.Path} {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return errors.New("sessioncapture: invalid hint identity")
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if len(body) > MaxHintBytes {
		return ErrQuota
	}
	return nil
}

func privateInboxPath(path string, dir bool) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0o077 != 0 || st.IsDir() != dir || (!dir && !st.Mode().IsRegular()) {
		return errors.New("sessioncapture: unsafe hint inbox")
	}
	return nil
}

func lockInbox(ctx context.Context, spool string) (string, *flock.Flock, error) {
	return lockNamedInbox(ctx, spool, "hints")
}

func lockNamedInbox(ctx context.Context, spool, name string) (string, *flock.Flock, error) {
	if err := os.MkdirAll(spool, 0o700); err != nil {
		return "", nil, err
	}
	if err := privateInboxPath(spool, true); err != nil {
		return "", nil, err
	}
	dir := filepath.Join(spool, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	if err := privateInboxPath(dir, true); err != nil {
		return "", nil, err
	}
	lockPath := filepath.Join(spool, name+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		err = f.Close()
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return "", nil, err
	}
	if err := privateInboxPath(lockPath, false); err != nil {
		return "", nil, err
	}
	lock := flock.New(lockPath)
	ok, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, ctx.Err()
	}
	if err = syncQueueTree(dir); err != nil {
		_ = lock.Unlock()
		return "", nil, err
	}
	return dir, lock, nil
}

func (q *Queue) drainHints(ctx context.Context) error {
	if err := q.InitRuntime(ctx); err != nil {
		return err
	}
	dir, lock, err := lockInbox(ctx, q.dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	// ReadDir(n) bounds memory independently of directory size. The independent
	// writer lock prevents replacement between DB acceptance and removal.
	entries, readErr := f.ReadDir(MaxInboxHints + 1)
	closeErr := f.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	var rejected, pressure int
	for _, entry := range entries {
		if len(entry.Name()) != 69 || !strings.HasSuffix(entry.Name(), ".json") {
			rejected++
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := q.acceptInboxHint(ctx, path); err != nil {
			// Never let an externally malformed entry or metadata pressure block
			// unrelated accepted hints. Retain it for diagnosis; do not acknowledge.
			if errors.Is(err, errInvalidHint) {
				rejected++
				continue
			}
			if errors.Is(err, ErrQuota) {
				pressure++
				continue
			}
			return err
		}
	}
	if _, err := q.db.ExecContext(ctx, `UPDATE runtime_discovery SET rejected_hints=?,backpressure_hints=?,inbox_truncated=? WHERE id=1`, rejected, pressure, len(entries) > MaxInboxHints); err != nil {
		return err
	}
	return syncDir(dir)
}

func (q *Queue) inboxLoop(ctx context.Context) error {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		if err := q.drainHints(ctx); err != nil {
			return runtimeStopError(ctx, err)
		}
		if err := q.pollDiscoveryHints(ctx); err != nil {
			return runtimeStopError(ctx, err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (q *Queue) acceptInboxHint(ctx context.Context, path string) error {
	if err := privateInboxPath(path, false); err != nil {
		return errInvalidHint
	}
	input, e := os.Open(path)
	if e != nil {
		return e
	}
	body, e := io.ReadAll(io.LimitReader(input, MaxHintBytes+1))
	e = errors.Join(e, input.Close())
	if e != nil {
		return e
	}
	var req Request
	if len(body) > MaxHintBytes {
		return errInvalidHint
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return errInvalidHint
	}
	if validateHint(req) != nil || req.Identity.ArchiveID != q.opts.ArchiveID || filepath.Base(path) != digest([]byte(req.Identity.BaseKey()+"\x00"+req.SourceID))+".json" {
		return errInvalidHint
	}
	if err := q.Hint(ctx, req); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func boundedInboxEntries(dir string) ([]os.DirEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(MaxInboxHints + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, errors.Join(err, f.Close())
}
