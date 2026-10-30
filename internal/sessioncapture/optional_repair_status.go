package sessioncapture

import "context"

type OptionalRepairStatus struct {
	ID              string
	SourceKeyHash   string
	EpochID         string
	State           string
	Attempts        int
	NextRetryNS     int64
	LastError       string
	Stage           string
	Contract        string
	SoftwareVersion string
}

func (q *Queue) OptionalRepairWorkStatus(ctx context.Context, afterID string, limit int) ([]OptionalRepairStatus, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT id,source_hash,epoch,state,attempts,next_retry,last_error,stage,contract,software FROM optional_repairs WHERE id>? ORDER BY id LIMIT ?`, afterID, min(max(limit, 1), 256))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []OptionalRepairStatus
	for rows.Next() {
		var s OptionalRepairStatus
		if err := rows.Scan(&s.ID, &s.SourceKeyHash, &s.EpochID, &s.State, &s.Attempts, &s.NextRetryNS, &s.LastError, &s.Stage, &s.Contract, &s.SoftwareVersion); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func ReadOptionalRepairWorkStatus(ctx context.Context, spool, archive, afterID string, limit int) ([]OptionalRepairStatus, error) {
	q, err := readDiagnosticQueue(ctx, spool, archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = q.db.Close() }()
	return q.OptionalRepairWorkStatus(ctx, afterID, limit)
}

// Replay keeps the row's attempts/last_error and original coordinates; it cannot clear the
// unavailable latch without a subsequent successful policy+remote integrity check.
func (q *Queue) ReplayOptionalRepair(ctx context.Context, id string) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE optional_repairs SET state='pending',next_retry=0 WHERE id=? AND state NOT IN ('working','delivering')`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrIntegrity
	}
	return tx.Commit()
}
