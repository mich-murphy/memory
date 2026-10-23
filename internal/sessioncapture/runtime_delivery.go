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
	if i.ArchiveID != q.opts.ArchiveID {
		return errors.New("sessioncapture: archive mismatch")
	}
	paused, err := q.destinationPaused(ctx)
	if err != nil {
		return err
	}
	if paused {
		return nil
	}
	optional, err := q.optionalDeliveryHead(ctx, i)
	if err != nil {
		return err
	}
	lock := q.deliveryLane(i, optional)
	// Never consume a worker budget waiting behind another capture/delivery.
	if !lock.TryLock() {
		return nil
	}
	defer lock.Unlock()
	var seq uint64
	var body, ev []byte
	var attempts int
	var state string
	var next int64
	err = q.db.QueryRowContext(ctx, `SELECT sequence,manifest,evidence,attempts,state,next_retry FROM jobs WHERE session=? AND state NOT IN ('published','superseded') ORDER BY sequence LIMIT 1`, i.BaseKey()).Scan(&seq, &body, &ev, &attempts, &state, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "quarantined" || next > q.opts.Now().UnixNano() {
		return nil
	}
	attemptErr := q.attempt(ctx, b, i, seq, body, ev)
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
	defer cancel()
	if errors.Is(attemptErr, errSelectionSuperseded) {
		return nil
	}
	if attemptErr != nil {
		return q.runtimeDeliveryFailure(save, i, seq, attempts+1, attemptErr, o)
	}
	if err = q.fault("ack-before-commit"); err != nil {
		return q.runtimeDeliveryFailure(save, i, seq, attempts+1, err, o)
	}
	_, err = q.db.ExecContext(save, `UPDATE jobs SET state='published',last_error='',next_retry=0 WHERE session=? AND sequence=? AND state!='superseded'`, i.BaseKey(), seq)
	if err != nil {
		return err
	}
	return q.fault("ack-committed")
}

func (q *Queue) runtimeDeliveryFailure(ctx context.Context, i publication.Identity, seq uint64, attempts int, cause error, o RuntimeOptions) (result error) {
	defer func() { result = errors.Join(runtimeFatalCause(cause), result) }()
	code := classify(cause, o.Classify)
	if code == FailureDestination {
		// Destination failures do not spend per-session poison budgets. Even repeated
		// operator resumes cannot quarantine otherwise healthy session evidence.
		if err := q.pauseDestination(ctx); err != nil {
			return err
		}
		_, err := q.db.ExecContext(ctx, `UPDATE jobs SET attempts=MAX(attempts-1,0),state='retry',last_error='destination',next_retry=0 WHERE session=? AND sequence=?`, i.BaseKey(), seq)
		return err
	}
	state := "retry"
	if attempts >= o.MaxAttempts {
		state = "quarantined"
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if state == "quarantined" {
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_quarantine(kind,session,sequence,reason,attempts,created_at,contract,software_version) VALUES('delivery',?,?,?,?,?,?,?)`, i.BaseKey(), seq, string(code), attempts, q.opts.Now().UnixNano(), publication.Contract, q.runtimeSoftwareVersion())
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET state=?,last_error=?,next_retry=? WHERE session=? AND sequence=?`, state, string(code), q.opts.Now().Add(retryDelay(attempts, o.Jitter)).UnixNano(), i.BaseKey(), seq)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
