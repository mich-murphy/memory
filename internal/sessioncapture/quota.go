package sessioncapture

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mich-murphy/memory/internal/publication"
)

// used includes durable payloads, crash orphans, temporary bytes and reservations
// not yet written. Only unwritten reservations or successfully removed duplicates
// may be released, plus payloads that reclaim.go proves unreferenced by every
// retained root (metadata decision committed first, then unlink; Open finishes
// interrupted unlinks before accounting). Crash orphans and unique optional or
// selection-superseded bytes are not reclaimed; a full spool still stops capture.
type quotaWriter struct {
	q        *Queue
	w        io.Writer
	bytes    int64
	retained bool
}

func (w *quotaWriter) Write(b []byte) (int, error) {
	size := int64(len(b))
	// Check the actual encoder stream before reserving quota or writing bytes.
	// Validate the cumulative count first so subtraction cannot overflow.
	if w.bytes < 0 || w.bytes > publication.MaxEncodedObjectBytes {
		return 0, ErrIntegrity
	}
	if size > publication.MaxEncodedObjectBytes-w.bytes {
		return 0, ErrEncodedSize
	}
	if err := w.q.reserve(size); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	w.q.mu.Lock()
	w.q.writing -= size
	w.q.used -= size - int64(n)
	w.q.mu.Unlock()
	w.bytes += int64(n)
	if n < len(b) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

func (q *Queue) reserve(n int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if n < 0 || q.used < 0 || q.writing < 0 || q.writing > q.used {
		return ErrIntegrity
	}
	if q.used > q.opts.MaxSpoolBytes || n > q.opts.MaxSpoolBytes-q.used {
		return ErrSpoolFull
	}
	if q.opts.MinFreeBytes > 0 {
		free, err := q.availableBytes()
		if err != nil {
			return err
		}
		// Existing writes are reflected in statfs, but outstanding reservations may
		// not be. Subtract them conservatively until their writes finish.
		if free < q.opts.MinFreeBytes || n > free-q.opts.MinFreeBytes-q.writing {
			return ErrSpoolFull
		}
		if err := q.checkWorkingReserveLocked(free, n); err != nil {
			return err
		}
	}
	q.used += n
	q.writing += n
	return nil
}

func (q *Queue) cleanupTemporary(path string, w *quotaWriter) {
	if err := q.fault("payload-before-cleanup"); err != nil {
		return
	}
	q.payloadMu.Lock()
	err := os.Remove(path)
	q.payloadMu.Unlock()
	if err != nil && !os.IsNotExist(err) {
		return
	}
	if !w.retained {
		q.mu.Lock()
		q.used -= w.bytes
		q.mu.Unlock()
	}
}

// availableBytes reports free space for the queue directory. The unexported
// freeBytes field is an internal seam so boundary arithmetic can be tested
// exactly; production leaves it nil and reads statfs.
func (q *Queue) availableBytes() (int64, error) {
	if q.freeBytes != nil {
		return q.freeBytes(q.dir)
	}
	return availableBytes(q.dir)
}

// sweepTemporaries removes writer temporaries (capture-* without the .zst
// suffix) left in payloads/ by a process that exited before installing or
// cleaning them. It runs at Open under the process lock, so no live writer owns
// them. Durable payloads (*.zst) and everything outside payloads/ are untouched.
// It reports whether any entry was removed so the caller can fence the directory.
func sweepTemporaries(dir string) (bool, error) {
	payloads := filepath.Join(dir, "payloads")
	entries, err := os.ReadDir(payloads)
	if err != nil {
		return false, err
	}
	removed := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "capture-") || strings.HasSuffix(name, ".zst") || !e.Type().IsRegular() {
			continue
		}
		if err := os.Remove(filepath.Join(payloads, name)); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}

// reclaimAndAccount runs at Open under the process lock. A crash before payload
// install leaves writer temporaries; they are reclaimed before accounting so
// their bytes are not charged forever. Existing payloads must fit the quota.
func (q *Queue) reclaimAndAccount(ctx context.Context) error {
	swept, err := sweepTemporaries(q.dir)
	if err != nil {
		return err
	}
	if swept {
		if err := syncDir(filepath.Join(q.dir, "payloads")); err != nil {
			return err
		}
	}
	// Finish unlinks whose metadata decision committed before a crash. The usage
	// scan below then charges only what is physically present.
	if _, err := q.reclaimPending(ctx); err != nil {
		return err
	}
	used, err := q.usage()
	if err != nil {
		return err
	}
	if used > q.opts.MaxSpoolBytes {
		return ErrQuota
	}
	q.used = used
	return nil
}
