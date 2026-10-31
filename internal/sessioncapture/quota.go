package sessioncapture

import (
	"io"
	"os"

	"github.com/mich-murphy/memory/internal/publication"
)

// used includes durable payloads, crash orphans, temporary bytes and reservations
// not yet written. Only unwritten reservations or successfully removed duplicates
// may be released. No accepted/orphan payload is garbage collected.
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
		return ErrQuota
	}
	if q.opts.MinFreeBytes > 0 {
		free, err := availableBytes(q.dir)
		if err != nil {
			return err
		}
		// Existing writes are reflected in statfs, but outstanding reservations may
		// not be. Subtract them conservatively until their writes finish.
		if free < q.opts.MinFreeBytes || n > free-q.opts.MinFreeBytes-q.writing {
			return ErrQuota
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
