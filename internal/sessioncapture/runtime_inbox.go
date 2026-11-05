package sessioncapture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// inboxSynced records inbox directories whose ancestor tree this process has
// already fenced successfully, keyed by absolute path. inboxTreeSyncHook observes
// every actual tree sync (tests only; nil in production).
var (
	inboxSynced       sync.Map
	inboxTreeSyncHook atomic.Pointer[func(string)]
)

// inboxCreated reports whether path is absent, i.e. the next MkdirAll creates it.
func inboxCreated(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// lockNamedInbox returns the locked inbox. The directory tree is fenced
// (syncQueueTree) so a writer's later syncDir(inbox) also covers the inbox and lock
// entries: on the first lock of a directory in this process, and whenever this call
// created the spool, the inbox or the lock file. Later ticks over an existing,
// already fenced inbox skip it; removal fsyncs by callers are unaffected.
func lockNamedInbox(ctx context.Context, spool, name string) (string, *flock.Flock, error) {
	dir := filepath.Join(spool, name)
	lockPath := filepath.Join(spool, name+".lock")
	created, err := prepareInbox(spool, dir, lockPath)
	if err != nil {
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
	if err = fenceInbox(dir, lockPath, created); err != nil {
		_ = lock.Unlock()
		return "", nil, err
	}
	return dir, lock, nil
}

// prepareInbox creates and validates the spool, inbox directory and lock file and
// reports whether any of them was absent beforehand (so this call created it).
func prepareInbox(spool, dir, lockPath string) (bool, error) {
	created := false
	for _, p := range []string{spool, dir, lockPath} {
		c, err := inboxCreated(p)
		if err != nil {
			return false, err
		}
		created = created || c
	}
	for _, d := range []string{spool, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return false, err
		}
		if err := privateInboxPath(d, true); err != nil {
			return false, err
		}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		err = f.Close()
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return false, err
	}
	return created, privateInboxPath(lockPath, false)
}

// fencedInbox is the identity of an inbox directory and lock file already fenced.
type fencedInbox struct{ dir, lock os.FileInfo }

// fenceInbox syncs the tree unless this process already fenced this very directory
// and lock file (same inodes) and nothing was created by the current call. A
// directory replaced or recreated by another process has a new identity and is
// fenced again.
func fenceInbox(dir, lockPath string, created bool) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	di, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	li, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	if v, ok := inboxSynced.Load(abs); ok && !created {
		if f, isF := v.(fencedInbox); isF && os.SameFile(f.dir, di) && os.SameFile(f.lock, li) {
			return nil
		}
	}
	if hook := inboxTreeSyncHook.Load(); hook != nil {
		(*hook)(abs)
	}
	if err := syncQueueTree(abs); err != nil {
		inboxSynced.Delete(abs)
		return err
	}
	inboxSynced.Store(abs, fencedInbox{dir: di, lock: li})
	return nil
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
			if !transientStepError(ctx, false, err) {
				return runtimeStopError(ctx, err)
			}
			q.noteStepError(ctx, "inbox", err)
		}
		if err := q.pollDiscoveryHints(ctx); err != nil {
			if !transientStepError(ctx, false, err) {
				return runtimeStopError(ctx, err)
			}
			q.noteStepError(ctx, "inbox", err)
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
