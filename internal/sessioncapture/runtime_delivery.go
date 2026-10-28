package sessioncapture

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mich-murphy/memory/internal/publication"
)

// DeliverDue attempts at most the head pending publication of one session. Unlike
// explicit Deliver, it never bypasses NextRetry or a quarantined head.
func (q *Queue) DeliverDue(ctx context.Context, b publication.Backend, i publication.Identity) error {
	if err := q.InitRuntime(ctx); err != nil {
		return err
	}
	err := q.deliverDue(ctx, b, i, runtimeDefaults(RuntimeOptions{}))
	return errors.Join(err, ctx.Err())
}

func (q *Queue) deliverDue(ctx context.Context, b publication.Backend, i publication.Identity, o RuntimeOptions) error {
	_, err := q.deliverDueWork(ctx, b, i, o)
	return err
}

// deliverDueWork reports work only once an attempt is reached. Scheduling or
// finding an idle lane must not keep a runtime worker in its immediate loop.
func (q *Queue) deliverDueWork(ctx context.Context, b publication.Backend, i publication.Identity, o RuntimeOptions) (bool, error) {
	if i.ArchiveID != q.opts.ArchiveID {
		return false, errors.New("sessioncapture: archive mismatch")
	}
	paused, err := q.destinationPaused(ctx)
	if err != nil {
		return false, err
	}
	if paused {
		return false, nil
	}
	optional, err := q.optionalDeliveryHead(ctx, i)
	if err != nil {
		return false, err
	}
	lock := q.deliveryLane(i, optional)
	// Never consume a worker budget waiting behind another capture/delivery.
	if !lock.TryLock() {
		return false, nil
	}
	defer lock.Unlock()
	var seq uint64
	var body, ev []byte
	var attempts int
	var state string
	var next int64
	err = q.db.QueryRowContext(ctx, `SELECT sequence,manifest,evidence,attempts,state,next_retry FROM jobs WHERE session=? AND state NOT IN ('published','superseded') ORDER BY sequence LIMIT 1`, i.BaseKey()).Scan(&seq, &body, &ev, &attempts, &state, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state == "quarantined" || next > q.opts.Now().UnixNano() {
		return false, nil
	}
	attemptErr := q.attempt(ctx, b, i, seq, body, ev)
	return true, q.deliveryResult(ctx, i, seq, attempts, attemptErr, o, false)
}

// Immediate delivery bypasses due time and returns the attempted error; daemon
// delivery respects due time and records retry/quarantine without surfacing a
// retryable error. Both persist the same guarded result and ACK transition.
func (q *Queue) deliveryResult(ctx context.Context, i publication.Identity, seq uint64, attempts int, cause error, o RuntimeOptions, immediate bool) error {
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
	defer cancel()
	if errors.Is(cause, errSelectionSuperseded) {
		if immediate && ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	if shutdownCancellation(ctx, cause) {
		_, err := q.db.ExecContext(save, `UPDATE jobs SET attempts=?,state='retry',last_error='shutdown',next_retry=0 WHERE session=? AND sequence=? AND state NOT IN ('published','superseded')`, attempts, i.BaseKey(), seq)
		return errors.Join(err, cause)
	}
	if cause == nil {
		cause = q.acknowledge(save, i, seq)
		if cause == nil {
			return nil
		}
		// A post-commit fault reports an ambiguous ACK, never a new failed job.
		var state string
		if err := q.db.QueryRowContext(save, `SELECT state FROM jobs WHERE session=? AND sequence=?`, i.BaseKey(), seq).Scan(&state); err != nil {
			return errors.Join(cause, err)
		}
		if state == "published" || state == "superseded" {
			return cause
		}
	}
	if immediate {
		return errors.Join(cause, q.persistDeliveryFailure(save, i, seq, attempts+1, cause, o, true))
	}
	return q.runtimeDeliveryFailure(save, i, seq, attempts+1, cause, o)
}

func (q *Queue) runtimeDeliveryFailure(ctx context.Context, i publication.Identity, seq uint64, attempts int, cause error, o RuntimeOptions) error {
	return q.persistDeliveryFailure(ctx, i, seq, attempts, cause, o, false)
}

func (q *Queue) persistDeliveryFailure(ctx context.Context, i publication.Identity, seq uint64, attempts int, cause error, o RuntimeOptions, immediate bool) (result error) {
	defer func() { result = errors.Join(runtimeFatalCause(cause), result) }()
	code := classify(cause, o.Classify)
	if code == FailureDestination {
		// Destination failures do not spend per-session poison budgets. Even repeated
		// operator resumes cannot quarantine otherwise healthy session evidence.
		if err := q.pauseDestination(ctx); err != nil {
			return err
		}
		_, err := q.db.ExecContext(ctx, `UPDATE jobs SET attempts=MAX(attempts-1,0),state='retry',last_error='destination',next_retry=0 WHERE session=? AND sequence=? AND state NOT IN ('published','superseded')`, i.BaseKey(), seq)
		return err
	}
	state := "retry"
	if immediate && (code == FailurePolicy || code == FailureIntegrity) {
		state = string(code) // Explicit replay retains the legacy visible blocked state.
	} else if !immediate && attempts >= o.MaxAttempts {
		state = "quarantined"
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?,last_error=?,next_retry=? WHERE session=? AND sequence=? AND state NOT IN ('published','superseded')`, state, string(code), q.opts.Now().Add(retryDelay(attempts, o.Jitter)).UnixNano(), i.BaseKey(), seq)
	if err != nil {
		return err
	}
	n, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if state == "quarantined" && n != 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_quarantine(kind,session,sequence,reason,attempts,created_at,contract,software_version) VALUES('delivery',?,?,?,?,?,?,?)`, i.BaseKey(), seq, string(code), attempts, q.opts.Now().UnixNano(), publication.Contract, q.runtimeSoftwareVersion()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
