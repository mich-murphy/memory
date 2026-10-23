package sessioncapture

import (
	"context"
	"fmt"

	"github.com/mich-murphy/memory/internal/publication"
)

const (
	MaxOptionalRepairRecords          = 4096
	MaxOptionalRepairHistory          = 32768
	MaxOptionalRepairHistoryPerRecord = 128
)

func (q *Queue) initOptionalRepairCaps(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS optional_repair_history_owner ON optional_repair_history(repair)`,
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS optional_repair_record_cap BEFORE INSERT ON optional_repairs WHEN NOT EXISTS(SELECT 1 FROM optional_repairs WHERE id=NEW.id) BEGIN SELECT CASE WHEN (SELECT count(*) FROM optional_repairs)>=%d THEN RAISE(ABORT,'sessioncapture: metadata quota exceeded') END; END`, MaxOptionalRepairRecords),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS optional_repair_history_cap BEFORE INSERT ON optional_repair_history BEGIN SELECT CASE WHEN (SELECT count(*) FROM optional_repair_history)>=%d OR (SELECT count(*) FROM optional_repair_history WHERE repair=NEW.repair)>=%d THEN RAISE(ABORT,'sessioncapture: metadata quota exceeded') END; END`, MaxOptionalRepairHistory, MaxOptionalRepairHistoryPerRecord),
	} {
		if _, err := q.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// Called with the session mutation lock. The accepted job already reserves the
// private source descriptors and payloads. Its fixed-size gap flag is the terminal
// reserve when no new repair/history row can be admitted. No evidence is evicted,
// no failed job is ACKed, and future raw captures retain the withheld coordinates.
// This is terminal/inspectable via Status.OptionalRepairGap, not accepted retry work.
func (q *Queue) terminalOptionalQuota(ctx context.Context, i publication.Identity) (bool, error) {
	if _, err := q.db.ExecContext(ctx, `UPDATE jobs SET optional_repair_gap='quota' WHERE session=? AND sequence=(SELECT max(sequence) FROM jobs WHERE session=?)`, i.BaseKey(), i.BaseKey()); err != nil {
		return false, err
	}
	if _, err := q.db.ExecContext(ctx, `UPDATE optional_repairs SET state='quarantined',last_error='quota',next_retry=0 WHERE session=? AND state='delivering'`, i.BaseKey()); err != nil {
		return false, err
	}
	changed, err := q.supersedeOptional(ctx, i)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, ErrIntegrity
	}
	return true, errSelectionSuperseded
}

// ReplayOptionalRepairQuota admits retained coordinates before clearing the
// terminal gap. Quota failure preserves the gap; success schedules autonomous
// policy/integrity/remote verification, never immediate selection or a false ACK.
func (q *Queue) ReplayOptionalRepairQuota(ctx context.Context, i publication.Identity) error {
	unlock, err := q.lockSession(ctx, i)
	if err != nil {
		return err
	}
	defer unlock()
	m, es, err := q.latest(ctx, i)
	if err != nil {
		return err
	}
	if m.Identity != i || m.Sequence == 0 || len(m.Sources) != len(es) {
		return ErrIntegrity
	}
	for n := range m.Sources {
		src, err := rehydrateSource(m.Sources[n], es[n])
		if err != nil {
			return err
		}
		if !src.Optional {
			continue
		}
		if err := q.persistOptionalRepair(ctx, i, src, es[n], "replay"); err != nil {
			return metadataError(err)
		}
	}
	_, err = q.db.ExecContext(ctx, `UPDATE jobs SET optional_repair_gap='' WHERE session=?`, i.BaseKey())
	return err
}
