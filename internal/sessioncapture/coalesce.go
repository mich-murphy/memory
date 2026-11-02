package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/mich-murphy/memory/internal/publication"
)

// supersedeSubsumed marks earlier, still-undelivered jobs of the new view's
// session as superseded when the new view provably contains every byte they
// would publish (contract section 3: coalesce proven-subsumed append captures).
// It runs inside the acceptance transaction, so the new job and the superseded
// marks commit atomically: a crash leaves either both or neither.
//
// Only plain pending/retry jobs are candidates. Published, quarantined, blocked,
// enrichment-gap and optional-repair jobs are never touched. A job whose delivery
// is in flight may be superseded: the CompareAndSwap guard and attemptManifest
// re-read the state, and an acknowledgement never overrides 'superseded', so the
// stale job cannot become the visible current after the newer one is accepted.
// The newer job is delivered afterwards under the same per-session lane and
// publishes above any sequence the stale job managed to commit first.
func supersedeSubsumed(ctx context.Context, tx *sql.Tx, next publication.Manifest) error {
	covered, err := subsumedCandidates(ctx, tx, next)
	if err != nil {
		return err
	}
	for _, seq := range covered {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state='superseded' WHERE session=? AND sequence=? AND state IN ('pending','retry')`, next.Identity.BaseKey(), seq); err != nil {
			return err
		}
	}
	return nil
}

// subsumedCandidates lists the sequences of undelivered plain jobs that next covers.
func subsumedCandidates(ctx context.Context, tx *sql.Tx, next publication.Manifest) (covered []uint64, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT sequence,manifest,state,enrichment_gap,optional_repair_gap FROM jobs WHERE session=? AND sequence<? AND state NOT IN ('published','superseded')`, next.Identity.BaseKey(), next.Sequence)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var seq uint64
		var body []byte
		var state, enrichmentGap, repairGap string
		if err := rows.Scan(&seq, &body, &state, &enrichmentGap, &repairGap); err != nil {
			return nil, err
		}
		// Only plain undelivered jobs: quarantined, blocked, enrichment-gap and
		// optional-repair jobs keep their own recovery path.
		if (state != "pending" && state != "retry") || enrichmentGap != "" || repairGap != "" {
			continue
		}
		var old publication.Manifest
		// An unreadable manifest is deliberately left alone, not superseded: delivery
		// reports it as an integrity failure. Not superseding is the safe direction.
		if json.Unmarshal(body, &old) == nil && subsumes(next, old) {
			covered = append(covered, seq)
		}
	}
	return covered, rows.Err()
}

// subsumes reports whether publishing next makes publishing old redundant: every
// source epoch of old is present in next with at least the same bytes. Withheld
// and optional sources are never proven here; those jobs keep their own path.
func subsumes(next, old publication.Manifest) bool {
	if next.Identity != old.Identity || next.Sequence <= old.Sequence {
		return false
	}
	index := make(map[[2]string]publication.Source, len(next.Sources))
	for i := range next.Sources {
		n := &next.Sources[i]
		if n.Withheld == nil && !n.Optional {
			index[[2]string{n.SourceID, n.EpochID}] = *n
		}
	}
	for i := range old.Sources {
		s := &old.Sources[i]
		if s.Withheld != nil || s.Optional {
			return false
		}
		n, ok := index[[2]string{s.SourceID, s.EpochID}]
		if !ok || !epochCovers(*s, n) {
			return false
		}
	}
	return true
}

// epochCovers compares one epoch across two views. A sealed epoch is immutable, so
// it must be byte-identical. An active epoch only ever grows by proven append
// (same epoch id implies a verified prefix at every step), so a later revision
// with at least as many bytes contains the earlier one; the same revision must be
// the same object. The newer view may have sealed the epoch since.
func epochCovers(old, next publication.Source) bool {
	same := publication.ObjectCommitment(old.Object) == publication.ObjectCommitment(next.Object)
	if old.State == "sealed" {
		return next.State == "sealed" && same
	}
	if old.State != "active" || next.Revision < old.Revision || next.Object.Size < old.Object.Size {
		return false
	}
	return next.Revision > old.Revision || same
}
