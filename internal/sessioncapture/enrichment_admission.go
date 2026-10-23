package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/mich-murphy/memory/internal/publication"
)

func (q *Queue) initEnrichmentAdmission(ctx context.Context) error {
	rows, err := q.db.QueryContext(ctx, `PRAGMA table_info(jobs)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	scanErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(scanErr, closeErr); err != nil {
		return err
	}
	for _, c := range []struct{ name, kind string }{{"optional_repair_gap", "TEXT NOT NULL DEFAULT ''"}, {"enrichment_gap", "TEXT NOT NULL DEFAULT ''"}, {"enrichment_intent", "BLOB"}} {
		if !columns[c.name] {
			if _, err := q.db.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN `+c.name+` `+c.kind); err != nil {
				return err
			}
		}
	}
	_, err = q.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS enrichment_admission_pending ON jobs(session,sequence) WHERE enrichment_gap!=''`)
	return err
}

func (q *Queue) admitCapturedEnrichment(ctx context.Context, tx *sql.Tx, m publication.Manifest, spec EnrichmentSpec) error {
	if _, err := validateSpec(spec); err != nil {
		return err
	}
	sourceIndex := -1
	for i := range m.Sources {
		if m.Sources[i].SourceID == spec.Source.SourceID && m.Sources[i].EpochID == spec.Source.EpochID {
			sourceIndex = i
			break
		}
	}
	if sourceIndex < 0 {
		return ErrIntegrity
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT enrichment_admission`); err != nil {
		return err
	}
	admissionErr := metadataError(enqueueEnrichmentTx(ctx, tx, spec))
	if admissionErr != nil {
		if !errors.Is(admissionErr, ErrQuota) {
			return admissionErr
		}
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO enrichment_admission`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `RELEASE enrichment_admission`); err != nil {
		return err
	}
	if admissionErr != nil {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET enrichment_gap='quota',enrichment_intent=? WHERE session=? AND sequence=?`, []byte(strconv.Itoa(sourceIndex)), m.Identity.BaseKey(), m.Sequence)
		return err
	}
	return nil
}

// A rejected source-enrichment intent lives with its accepted raw job. It is not
// accepted enrichment work and can be adopted independently after capacity repair.
func (q *Queue) retryEnrichmentAdmission(ctx context.Context) error {
	var session string
	var sequence uint64
	var body []byte
	err := q.db.QueryRowContext(ctx, `SELECT session,sequence,enrichment_intent FROM jobs WHERE enrichment_gap!='' ORDER BY sequence,session LIMIT 1`).Scan(&session, &sequence, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	index, err := strconv.Atoi(string(body))
	if err != nil {
		return ErrIntegrity
	}
	var manifestBody, evidenceBody []byte
	if err := q.db.QueryRowContext(ctx, `SELECT manifest,evidence FROM jobs WHERE session=? AND sequence=?`, session, sequence).Scan(&manifestBody, &evidenceBody); err != nil {
		return err
	}
	var m publication.Manifest
	var es []evidence
	if json.Unmarshal(manifestBody, &m) != nil || json.Unmarshal(evidenceBody, &es) != nil || index < 0 || index >= len(m.Sources) || len(es) != len(m.Sources) {
		return ErrIntegrity
	}
	spec, ok := capturedSpec(m, es, m.Sources[index].SourceID)
	if !ok {
		return ErrIntegrity
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := metadataError(enqueueEnrichmentTx(ctx, tx, spec)); err != nil {
		if errors.Is(err, ErrQuota) {
			return nil
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET enrichment_gap='',enrichment_intent=NULL WHERE session=? AND sequence=?`, session, sequence); err != nil {
		return err
	}
	return tx.Commit()
}
