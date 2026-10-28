package sessioncapture

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/scrub"
)

// PreparedWorkingBytes is separate from MaxSpoolBytes (encoded payloads only).
// Four nonwaiting slots each reserve a full MaxSourceBytes, even for small jobs.
// MinFreeBytes additionally reserves remaining encoded spool headroom while any
// prepared job is active. Filesystem overhead/SQLite journals need separate space;
// external disk writers remain outside this queue reservation protocol.
// Crash/cleanup orphans retain their entire slot charge on restart; there is no
// automatic GC. Exhaustion needs operator inspection, never deletion of payloads.
// This bounds prepared disk to 4GiB, NOT 256MiB. Config parsing additionally uses
// bounded 5MiB input plus parser/redaction allocations. zstd uses a single decoder
// with a 64MiB window/memory bound; SQLite steps one page and uses its page cache.
const (
	PreparedWorkingBytes       = 4 * MaxSourceBytes
	configSourceLimit    int64 = 5 << 20
)

// derivedReservations is private queue-local state. q.mu protects active; the
// channel bounds callers before any prepared-directory filesystem inspection.
type derivedReservations struct {
	workers chan struct{}
	active  int64
}

func (q *Queue) derivedReservations() *derivedReservations {
	value, _ := q.installs.LoadOrStore("derived-reservations-state", &derivedReservations{workers: make(chan struct{}, 4)})
	state, ok := value.(*derivedReservations)
	if !ok {
		panic("sessioncapture: invalid derived reservation state")
	}
	return state
}

// checkWorkingReserveLocked is called with q.mu held, including from the encoded
// quota reservation path. Full working slots remain reserved until cleanup; while
// any slot is active we also reserve ALL remaining encoded spool headroom. This
// intentionally overcounts written working bytes and can reject small captures on
// nearly full disks. It never treats encoded MaxSpoolBytes as a working budget.
func (q *Queue) checkWorkingReserveLocked(free, n int64) error {
	state := q.derivedReservations()
	if state.active == 0 || q.opts.MinFreeBytes == 0 {
		return nil
	}
	remaining := q.opts.MaxSpoolBytes - q.used - n
	if remaining < 0 {
		return ErrQuota
	}
	working := state.active * MaxSourceBytes
	// Subtractions avoid overflow for operator-supplied byte budgets.
	for _, amount := range []int64{q.opts.MinFreeBytes, q.writing, n, working, remaining} {
		if amount < 0 || amount > free {
			return ErrQuota
		}
		free -= amount
	}
	return nil
}

func (q *Queue) preparedSlot() (string, func() error, error) {
	state := q.derivedReservations()
	select {
	case state.workers <- struct{}{}:
	default:
		return "", nil, ErrQuota
	}
	path, err := q.reservePreparedSlot(state)
	if err != nil {
		<-state.workers
		return "", nil, err
	}
	return path, func() error {
		cleanupErr := q.fault("prepared-before-cleanup")
		q.mu.Lock()
		// Failed removal retains its on-disk slot charge on later captures/restart.
		// It has no future writes, so actual allocated disk is reflected by statfs.
		if cleanupErr == nil {
			cleanupErr = os.RemoveAll(path)
		}
		state.active--
		q.mu.Unlock()
		<-state.workers
		return cleanupErr
	}, nil
}

func (q *Queue) reservePreparedSlot(state *derivedReservations) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	root := filepath.Join(q.dir, "prepared")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0o077 != 0 {
		return "", ErrIntegrity
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	if len(entries) >= int(PreparedWorkingBytes/MaxSourceBytes) {
		return "", ErrQuota
	}
	state.active++
	if err := q.checkPreparedSpaceLocked(); err != nil {
		state.active--
		return "", err
	}
	for i := range int(PreparedWorkingBytes / MaxSourceBytes) {
		path := filepath.Join(root, strconv.Itoa(i))
		if err := os.Mkdir(path, 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			state.active--
			return "", err
		}
		return path, nil
	}
	state.active--
	return "", ErrQuota
}

func (q *Queue) checkPreparedSpaceLocked() error {
	if q.opts.MinFreeBytes == 0 {
		return nil
	}
	free, err := availableBytes(q.dir)
	if err != nil {
		return err
	}
	return q.checkWorkingReserveLocked(free, 0)
}

func (q *Queue) workingSpace() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.checkPreparedSpaceLocked()
}

type preparedWriter struct {
	q *Queue
	w io.Writer
	n int64
}

func (w *preparedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > MaxSourceBytes-w.n {
		return 0, ErrQuota
	}
	if err := w.q.workingSpace(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	w.n += int64(n)
	if n < len(b) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

// materialize is exclusively internal: public callers cannot submit a prepared
// pathname in place of the original protected source. Its result is immutable
// during capture, flushed with checked Sync/Close before snapshot reads it.
// Prepared files are disposable, not acknowledged power-loss byte ownership;
// only the existing payload+job acceptance boundary establishes that ownership.
func (q *Queue) materialize(ctx context.Context, r Request, source *authorizedSource) (authorizedSource, func() error, error) {
	if r.Transform == "" {
		return *source, func() error { return nil }, nil
	}
	slot, cleanup, err := q.preparedSlot()
	if err != nil {
		return authorizedSource{}, nil, err
	}
	path := filepath.Join(slot, "body")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return authorizedSource{}, nil, errors.Join(err, cleanup())
	}
	pw := &preparedWriter{q: q, w: f}
	if r.Transform == "sqlite-backup" {
		err = q.backupSQLiteAuthorized(ctx, r, source, path)
	} else {
		err = q.transformFile(ctx, r, *source, pw)
	}
	if err == nil {
		err = q.fault("prepared-before-sync")
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return authorizedSource{}, nil, errors.Join(err, cleanup())
	}
	prepared, err := observePrivate(path)
	if err != nil {
		return authorizedSource{}, nil, errors.Join(err, cleanup())
	}
	return prepared, cleanup, nil
}

func (q *Queue) transformFile(ctx context.Context, r Request, source authorizedSource, out io.Writer) error {
	resolution, err := q.check(ctx, evidence{Request: r}, false)
	if err != nil {
		return err
	}
	if resolution.Path != source.Path {
		return ErrPolicy
	}
	f, err := source.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // Read-only source.
	before, err := f.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("%w: regular derived source required", ErrPolicy)
	}
	limit := MaxSourceBytes
	if r.Transform == "scrubbed-config" {
		limit = configSourceLimit
	}
	if before.Size() > limit {
		return ErrQuota
	}
	h := sha256.New()
	n, err := io.Copy(h, &contextReader{ctx: ctx, r: io.LimitReader(f, limit+1)})
	if err != nil {
		return err
	}
	if n > limit {
		return ErrQuota
	}
	if err := stableObservationSource(ctx, f, source.Path, before, n, sum(h), source.original); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := writeTransformed(ctx, r, resolution.Identities, f, out); err != nil {
		return err
	}

	return stableObservationSource(ctx, f, source.Path, before, n, sum(h), source.original)
}

func writeTransformed(ctx context.Context, r Request, identities []string, f *os.File, out io.Writer) error {
	switch r.Transform {
	case "zstd":
		zr, err := zstd.NewReader(&contextReader{ctx: ctx, r: io.LimitReader(f, MaxSourceBytes+1)}, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20), zstd.WithDecoderMaxWindow(64<<20))
		if err != nil {
			return err
		}
		_, err = io.Copy(out, &contextReader{ctx: ctx, r: io.LimitReader(zr, MaxSourceBytes+1)})
		zr.Close()
		if err != nil {
			return err
		}
	case "scrubbed-config":
		body, err := io.ReadAll(&contextReader{ctx: ctx, r: io.LimitReader(f, configSourceLimit+1)})
		if err != nil {
			return err
		}
		if int64(len(body)) > configSourceLimit {
			return ErrQuota
		}
		body, err = scrub.Identities(r.Identity.Agent, identities, body)
		if err != nil {
			return fmt.Errorf("%w: protected configuration scrub failed", ErrPolicy)
		}
		if _, err = out.Write(body); err != nil {
			return err
		}
	default:
		return ErrPolicy
	}
	return nil
}

func (q *Queue) latestPolicyChecked(ctx context.Context, identity publication.Identity) (publication.Manifest, []evidence, error) {
	m, es, err := q.latest(ctx, identity)
	if err != nil {
		return m, es, err
	}
	if len(m.Sources) != len(es) {
		return m, es, ErrIntegrity
	}
	for i := range es {
		if m.Sources[i].Withheld != nil {
			es[i].SelectionReason = m.Sources[i].Withheld.Reason
		}
		source, err := rehydrateSource(m.Sources[i], es[i])
		if err != nil {
			return m, es, err
		}
		m.Sources[i] = source

		if m.Sources[i].Optional {
			continue
		}
		if _, err := q.check(ctx, es[i], true); err != nil {
			return m, es, err
		}
	}
	return m, es, nil
}

func (q *Queue) validateUnchangedManifest(m publication.Manifest) error {
	if _, err := q.usage(); err != nil {
		return err
	}
	return publication.ValidateManifest(m)
}
