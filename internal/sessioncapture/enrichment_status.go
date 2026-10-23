package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
)

// EnrichmentStatus is a bounded private diagnostic page. Error values are closed
// classifications; callers must not print Spec paths in public diagnostics.
type EnrichmentStatus struct {
	ID               string
	Spec             EnrichmentSpec
	State            string
	Attempts         int
	NextRetryNS      int64
	LastError        string
	Contract         string
	SoftwareVersion  string
	AttachedSequence uint64
	ResultID         string
	Complete         bool
	Missing          []string
	BasisCurrent     bool
}

func (q *Queue) EnrichmentWorkStatus(ctx context.Context, afterID string, limit int) ([]EnrichmentStatus, error) {
	limit = min(max(limit, 1), 256)
	rows, err := q.db.QueryContext(ctx, `SELECT w.id,w.spec,w.state,w.attempts,w.next_retry,w.last_error,w.contract,w.software,w.attached_sequence,coalesce(CAST(w.result AS TEXT),''),coalesce(json_extract(r.body,'$.Complete'),0),coalesce(json_extract(r.body,'$.Missing'),'[]') FROM enrichment_work w LEFT JOIN enrichment_results r ON r.id=CAST(w.result AS TEXT) WHERE w.id>? ORDER BY w.id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EnrichmentStatus
	for rows.Next() {
		var s EnrichmentStatus
		var body, missing []byte
		if err := rows.Scan(&s.ID, &body, &s.State, &s.Attempts, &s.NextRetryNS, &s.LastError, &s.Contract, &s.SoftwareVersion, &s.AttachedSequence, &s.ResultID, &s.Complete, &missing); err != nil {
			return nil, err
		}
		if json.Unmarshal(body, &s.Spec) != nil || json.Unmarshal(missing, &s.Missing) != nil {
			return nil, ErrIntegrity
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		m, _, err := q.latest(ctx, out[i].Spec.Identity)
		if err != nil {
			return nil, err
		}
		for j := range m.Sources {
			s := &m.Sources[j]
			basis := out[i].Spec.Source
			if s.SourceID == basis.SourceID && s.EpochID == basis.EpochID && s.Revision == basis.Revision && s.Object.SHA256 == basis.SHA256 && s.State == "active" {
				out[i].BasisCurrent = true
			}
		}
	}
	return out, nil
}

// ReadEnrichmentWorkStatus is read-only and does not acquire the queue flock.
func ReadEnrichmentWorkStatus(ctx context.Context, spool, archive, afterID string, limit int) ([]EnrichmentStatus, error) {
	q, err := readDiagnosticQueue(ctx, spool, archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = q.db.Close() }()
	return q.EnrichmentWorkStatus(ctx, afterID, limit)
}

// ReplayEnrichment retains every failure/history row and retained result. Its
// subsequent attachment always rechecks policy/integrity and rebases latest.
func (q *Queue) ReplayEnrichment(ctx context.Context, id string) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM enrichment_work WHERE id=?`, id).Scan(&state); err != nil {
		return err
	}
	if state == "working" {
		return errors.New("sessioncapture: enrichment busy")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO enrichment_history(work,state,at,reason) VALUES(?,'replay',?,'operator')`, id, q.opts.Now().UnixNano()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE enrichment_work SET state='pending',next_retry=0 WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func readDiagnosticQueue(ctx context.Context, spool, archive string) (*Queue, error) {
	if err := privateInboxPath(spool, true); err != nil {
		return nil, err
	}
	path := filepath.Join(spool, "queue.sqlite")
	if err := privateInboxPath(path, false); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	var binding string
	if err := db.QueryRowContext(ctx, `SELECT archive FROM binding LIMIT 1`).Scan(&binding); err != nil {
		_ = db.Close()
		return nil, err
	}
	if archive == "" || binding != archive {
		_ = db.Close()
		return nil, ErrPolicy
	}
	return &Queue{db: db}, nil
}
