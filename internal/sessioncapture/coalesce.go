package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/mich-murphy/memory/internal/publication"
)

// retireJobs supersedes earlier undelivered jobs of next's session inside the
// acceptance transaction, so the new job and the superseded marks commit
// atomically: a crash leaves either both or neither. With subsumedOnly it
// supersedes only plain pending/retry jobs that next provably subsumes (contract
// section 3: coalesce proven-subsumed append captures). Otherwise it supersedes
// every undelivered job (selection supersession by enrichment/optional detach).
//
// Published, quarantined (subsumedOnly), blocked, enrichment-gap and
// optional-repair jobs are never coalesced. A job whose delivery is in flight may
// be superseded: the CompareAndSwap guard and attemptManifest re-read the state,
// and an acknowledgement never overrides 'superseded', so the stale job cannot
// become the visible current after the newer one is accepted. The newer job is
// delivered afterwards under the same per-session lane.
//
// Objects that only the superseded job held become reclamation candidates in the
// same transaction (the durable metadata decision); the unlink happens later.
func retireJobs(ctx context.Context, tx *sql.Tx, next publication.Manifest, subsumedOnly bool) error {
	jobs, err := undeliveredJobs(ctx, tx, next, subsumedOnly)
	if err != nil {
		return err
	}
	for i := range jobs {
		j := &jobs[i]
		if !j.retirable(next, subsumedOnly) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state='superseded' WHERE session=? AND sequence=? AND state NOT IN ('published','superseded')`, next.Identity.BaseKey(), j.sequence); err != nil {
			return err
		}
		// Selection supersession (enrichment attach or optional detach) retires the
		// job but not its bytes: optional artifacts and their raw companions are unique
		// local bytes that may never have been published, so they are kept. Only a
		// proven-subsumed append is redundant by construction.
		if subsumedOnly && j.readable {
			if err := addReclaimCandidates(ctx, tx, next.Identity.BaseKey(), retiredObjects(j.manifest, next)); err != nil {
				return err
			}
		}
	}
	return nil
}

type undeliveredJob struct {
	sequence uint64
	manifest publication.Manifest
	// readable is false for an unparsable manifest, and for a manifest that was
	// not loaded (not coalescing, or not plain). Such a job is never coalesced
	// (delivery reports the integrity failure) and contributes no candidates.
	readable bool
	// plain means pending/retry with no enrichment or optional-repair gap.
	plain bool
}

// undeliveredJobs lists the undelivered jobs before next. The manifest is
// fetched and parsed only where it can matter: a coalescing pass (subsumedOnly)
// over a plain job. Selection supersession retires every job without reading
// it, and a non-plain job is never coalesced, so neither needs a parse.
func undeliveredJobs(ctx context.Context, tx *sql.Tx, next publication.Manifest, subsumedOnly bool) (jobs []undeliveredJob, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT sequence,CASE WHEN ? AND state IN ('pending','retry') AND enrichment_gap='' AND optional_repair_gap='' THEN manifest END,state,enrichment_gap,optional_repair_gap FROM jobs WHERE session=? AND sequence<? AND state NOT IN ('published','superseded')`, subsumedOnly, next.Identity.BaseKey(), next.Sequence)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var j undeliveredJob
		var body []byte
		var state, enrichmentGap, repairGap string
		if err := rows.Scan(&j.sequence, &body, &state, &enrichmentGap, &repairGap); err != nil {
			return nil, err
		}
		j.plain = (state == "pending" || state == "retry") && enrichmentGap == "" && repairGap == ""
		if body != nil {
			j.readable = json.Unmarshal(body, &j.manifest) == nil
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
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

// retirable decides whether a job is superseded by next. Selection supersession
// (all=false in subsumedOnly terms) retires every undelivered job; coalescing
// retires only plain jobs next provably subsumes.
func (j *undeliveredJob) retirable(next publication.Manifest, subsumedOnly bool) bool {
	if !subsumedOnly {
		return true
	}
	return j.plain && j.readable && subsumes(next, j.manifest)
}
