package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// runtimeLog receives one line per swallowed work-step error. Tests replace it.
var runtimeLog io.Writer = os.Stderr

// runtimeReleaseTimeout bounds each claim-release/diagnostic write made after a
// failed step. It is a variable only so tests need not wait out real starvation.
var runtimeReleaseTimeout = 5 * time.Second

// transientStepError reports an error that must skip one work step but not stop
// Run: every leaf is an operation-local deadline, SQLITE_BUSY/LOCKED, or (only
// when opExpired) SQLITE_INTERRUPT raised by that expired operation context, and
// the Run context is still live. One fatal leaf (durability, EIO, cancellation,
// non-busy sqlite, policy, integrity) keeps the whole tree fatal.
func transientStepError(ctx context.Context, opExpired bool, err error) bool {
	return err != nil && ctx.Err() == nil && leavesAre(err, func(leaf error) bool {
		if leaf == context.DeadlineExceeded { //nolint:errorlint // Exact leaf only; wrappers are descended explicitly.
			return true
		}
		if se, ok := errors.AsType[*sqlite.Error](leaf); ok {
			code := se.Code() & 0xff
			return code == 5 || code == 6 || (code == 9 && opExpired)
		}
		return false
	})
}

// leavesAre is the single error-tree walker: true when err has leaves and all satisfy ok.
func leavesAre(err error, ok func(error) bool) bool {
	if joined, isJoin := err.(interface{ Unwrap() []error }); isJoin {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, c := range children {
			if !leavesAre(c, ok) {
				return false
			}
		}
		return true
	}
	if wrapped, isWrap := err.(interface{ Unwrap() error }); isWrap {
		return leavesAre(wrapped.Unwrap(), ok)
	}
	return ok(err)
}

// noteStepError makes a swallowed error visible: a stderr line plus a durable
// counter/last-error on the existing runtime_destination row (best effort).
func (q *Queue) noteStepError(ctx context.Context, worker string, err error) {
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	_, _ = fmt.Fprintf(runtimeLog, "capture-v1: worker=%s step skipped, will retry: %s\n", worker, msg)
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtimeReleaseTimeout)
	defer cancel()
	_, _ = q.db.ExecContext(save, `UPDATE runtime_destination SET step_errors=step_errors+1,last_step_error=?,last_step_error_at=? WHERE id=1`, worker+": "+msg, q.opts.Now().UnixNano())
}

func releaseCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), runtimeReleaseTimeout)
}

func (q *Queue) releaseSourceClaim(ctx context.Context, w sourceWork) {
	if w.id == 0 {
		return
	}
	save, cancel := releaseCtx(ctx)
	defer cancel()
	_, _ = q.db.ExecContext(save, "UPDATE runtime_sources SET claim=0 WHERE id=? AND claim=?", w.id, w.version)
}

func (q *Queue) releaseSessionClaim(ctx context.Context, id int64) {
	save, cancel := releaseCtx(ctx)
	defer cancel()
	_, _ = q.db.ExecContext(save, "UPDATE runtime_sessions SET claim=0 WHERE id=?", id)
}

func (q *Queue) releaseOptionalRepair(ctx context.Context, id string) {
	save, cancel := releaseCtx(ctx)
	defer cancel()
	_, _ = q.db.ExecContext(save, `UPDATE optional_repairs SET state='retry' WHERE id=? AND state='working'`, id)
}

func (q *Queue) releaseEnrichment(ctx context.Context, id string) {
	save, cancel := releaseCtx(ctx)
	defer cancel()
	_, _ = q.db.ExecContext(save, `UPDATE enrichment_work SET state='pending' WHERE id=? AND state='working'`, id)
}
