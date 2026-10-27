package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func (q *Queue) initOptionalRepair(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS optional_repairs(id TEXT PRIMARY KEY,session TEXT NOT NULL,source_hash TEXT NOT NULL,epoch TEXT NOT NULL,commitment TEXT NOT NULL,source BLOB NOT NULL,evidence BLOB NOT NULL,identity BLOB NOT NULL,state TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_retry INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',stage TEXT NOT NULL,contract TEXT NOT NULL,software TEXT NOT NULL)`,
		`UPDATE optional_repairs SET state='retry' WHERE state IN ('working','delivering')`,
	} {
		if _, err := q.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return q.initOptionalRepairCaps(ctx)
}

func repairID(i publication.Identity, s publication.Source) string {
	return digest([]byte(i.BaseKey() + "\x00" + publication.SourceKeyHash(s) + "\x00" + s.EpochID + "\x00" + publication.ObjectCommitment(s.Object)))
}

func (q *Queue) unavailableLatch(ctx context.Context, i publication.Identity, s publication.Source) (bool, error) {
	var state string
	err := q.db.QueryRowContext(ctx, `SELECT state FROM optional_repairs WHERE id=?`, repairID(i, s)).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return state != "repaired" && state != "verified_attachment_pending" && state != "delivering", err
}

// Network outcomes are saved with a fresh bounded local context. A timed-out
// operation must not lose the latch and leave raw delivery behind its old head.
func (q *Queue) isolateOptionalFailure(ctx context.Context, i publication.Identity, m publication.Manifest, es []evidence, cause error) (bool, error) {
	if cause == nil {
		return false, nil
	}
	if errors.Is(cause, ErrSessionExcluded) || runtimeFatalCause(cause) != nil ||
		errors.Is(cause, context.Canceled) ||
		(errors.Is(cause, context.DeadlineExceeded) && !genuineOptionalExpiry(ctx, cause)) ||
		q.optionalFailureClass(cause) == FailureDestination {
		return false, nil
	}
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false, nil
	}
	var dependency *publication.SourceDependencyError
	if errors.As(cause, &dependency) && dependency.Optional {
		return q.withholdUnavailable(ctx, i, m, es, dependency)
	}
	if !optionalFailure(cause) || ctx.Err() != nil || !q.hasDeniedOptional(ctx, m, es) {
		return false, nil
	}
	unlock, err := q.lockSession(ctx, i)
	if err != nil {
		return false, err
	}
	defer unlock()
	changed, err := q.supersedeOptional(ctx, i)
	if err != nil {
		return false, err
	}
	if changed {
		return true, errSelectionSuperseded
	}
	return false, nil
}

// genuineOptionalExpiry is the only deadline that may be isolated: an optional
// source dependency whose every leaf is a bare deadline, while the operation
// context itself has expired. A deadline under a live context stays fatal.
func genuineOptionalExpiry(ctx context.Context, cause error) bool {
	var dependency *publication.SourceDependencyError
	return errors.As(cause, &dependency) && dependency.Optional &&
		errors.Is(ctx.Err(), context.DeadlineExceeded) &&
		leavesAre(cause, func(e error) bool { return e == context.DeadlineExceeded })
}

func (q *Queue) withholdUnavailable(ctx context.Context, i publication.Identity, m publication.Manifest, es []evidence, dependency *publication.SourceDependencyError) (bool, error) {
	index := -1
	for n := range m.Sources {
		if publication.SourceKeyHash(m.Sources[n]) == dependency.SourceKeyHash && m.Sources[n].EpochID == dependency.EpochID && m.Sources[n].Optional && publication.IsSelected(m.Sources[n]) {
			index = n
			break
		}
	}
	if index < 0 || len(es) != len(m.Sources) {
		return false, ErrIntegrity
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	unlock, err := q.lockSession(save, i)
	if err != nil {
		return false, err
	}
	defer unlock()
	if err := q.persistOptionalRepair(save, i, m.Sources[index], es[index], dependency.Stage); err != nil {
		if errors.Is(metadataError(err), ErrQuota) {
			return q.terminalOptionalQuota(save, i)
		}
		return false, err
	}
	changed, err := q.supersedeOptional(save, i)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, ErrIntegrity
	}
	return true, errSelectionSuperseded
}

func (q *Queue) persistOptionalRepair(ctx context.Context, i publication.Identity, s publication.Source, e evidence, stage string) error {
	source, err := json.Marshal(s)
	if err != nil {
		return err
	}
	ev, err := json.Marshal(e)
	if err != nil {
		return err
	}
	identity, err := json.Marshal(i)
	if err != nil {
		return err
	}
	id := repairID(i, s)
	_, err = q.db.ExecContext(ctx, `INSERT INTO optional_repairs(id,session,source_hash,epoch,commitment,source,evidence,identity,stage,contract,software) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state='pending',stage=excluded.stage,last_error='unavailable',next_retry=0`, id, i.BaseKey(), publication.SourceKeyHash(s), s.EpochID, publication.ObjectCommitment(s.Object), source, ev, identity, stage, publication.Contract, q.runtimeSoftwareVersion())
	return err
}

func (q *Queue) optionalRepairNext(ctx context.Context, b publication.Backend, o RuntimeOptions) (bool, error) {
	paused, err := q.destinationPaused(ctx)
	if err != nil || paused {
		return false, err
	}
	var id string
	var source, ev, identity []byte
	var attempts int
	var state string
	err = q.db.QueryRowContext(ctx, `SELECT id,source,evidence,identity,attempts,state FROM optional_repairs WHERE state IN ('pending','retry','verified_attachment_pending') AND next_retry<=? ORDER BY attempts,next_retry,rowid LIMIT 1`, q.opts.Now().UnixNano()).Scan(&id, &source, &ev, &identity, &attempts, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var s publication.Source
	var e evidence
	var i publication.Identity
	if json.Unmarshal(source, &s) != nil || json.Unmarshal(ev, &e) != nil || json.Unmarshal(identity, &i) != nil {
		return true, ErrIntegrity
	}
	if state != "verified_attachment_pending" {
		if _, err := q.db.ExecContext(ctx, `UPDATE optional_repairs SET state='working',attempts=attempts+1 WHERE id=?`, id); err != nil {
			return true, err
		}
		// No-op once a terminal state was saved; frees 'working' after a failed save.
		defer q.releaseOptionalRepair(ctx, id)
		err = q.repairOptionalObject(ctx, b, s, e)
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
	defer cancel()
	if err != nil {
		return true, q.saveOptionalRepairFailure(save, id, attempts+1, err, o)
	}
	// Verification and attachment are separate durable facts. Recovery retries
	// this state even when no new hook arrives; it always rebases on latest.
	if _, err := q.db.ExecContext(save, `UPDATE optional_repairs SET state='verified_attachment_pending',next_retry=0 WHERE id=?`, id); err != nil {
		return true, err
	}
	if err := q.fault("optional-repair-verified"); err != nil {
		return true, err
	}
	unlock, err := q.lockSession(save, i)
	if err != nil {
		return true, err
	}
	defer unlock()
	if _, err = q.selectionView(save, i, false); err != nil {
		return true, err
	}
	_, err = q.db.ExecContext(save, `UPDATE optional_repairs SET state='repaired',last_error='',next_retry=0 WHERE id=?`, id)
	return true, err
}

func (q *Queue) repairOptionalObject(ctx context.Context, b publication.Backend, s publication.Source, e evidence) error {
	if _, err := q.check(ctx, e, true); err != nil {
		return err
	}
	if err := q.verify(ctx, s.Object); err != nil {
		return err
	}
	if _, err := q.check(ctx, e, true); err != nil {
		return err
	}
	if err := b.PutFile(ctx, s.Object.Key, q.payload(s.Object)); err != nil {
		return err
	}
	if err := publication.VerifySourceDependency(ctx, b, s); err != nil {
		return err
	}
	if durable, ok := b.(publication.DurabilityFence); ok {
		return durable.Sync(ctx, s.Object.Key)
	}
	return nil
}

func (q *Queue) saveOptionalRepairFailure(ctx context.Context, id string, attempts int, cause error, o RuntimeOptions) (result error) {
	defer func() { result = errors.Join(runtimeFatalCause(cause), result) }()
	if classify(cause, o.Classify) == FailureDestination {
		if err := q.pauseDestination(ctx); err != nil {
			return err
		}
		_, err := q.db.ExecContext(ctx, `UPDATE optional_repairs SET state='retry',attempts=MAX(attempts-1,0),last_error='destination',next_retry=0 WHERE id=?`, id)
		return err
	}
	state := "retry"
	if attempts >= o.MaxAttempts {
		state = "quarantined"
	}
	code := string(classify(cause, o.Classify))
	_, err := q.db.ExecContext(ctx, `UPDATE optional_repairs SET state=?,last_error=?,next_retry=? WHERE id=?`, state, code, q.opts.Now().Add(retryDelay(attempts, o.Jitter)).UnixNano(), id)
	return err
}

func (q *Queue) optionalFailureClass(cause error) FailureClass {
	options := q.runtimeLocal().enrichmentOptions.Load()
	if options != nil {
		return classify(cause, options.Classify)
	}
	return classify(cause, nil)
}
