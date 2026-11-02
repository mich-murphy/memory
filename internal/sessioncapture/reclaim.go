package sessioncapture

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mich-murphy/memory/internal/publication"
)

// Local payload reclamation (contract section 3: "remote acknowledgement + local
// reclamation"). It removes spool payload files that no retained root references.
// It never touches remote storage; remote retention and GC are a separate policy.
//
// The metadata decision is committed first: when a job stops being a root
// (superseded at acceptance, or an older published job replaced by a newer
// acknowledgement) the objects only that job held become rows of payload_reclaim
// in the same transaction. The unlink follows, under the per-hash install mutex.
// A crash between the two leaves a candidate row and a file; Open drains the rows
// before accounting, so usage is right whichever side of the unlink the crash hit.
//
// A candidate is kept when any root still references it: an undelivered job
// (pending, retry, claimed, blocked or quarantined) of its session, the latest job
// and the latest published job of its session, any other session that ever
// referenced the same content-addressed file, an enrichment result, or optional
// repair work that is not finished (the last two as indexed payload_refs holders). Payloads no job references (crash orphans and
// enrichment artifacts before attachment) are never candidates and stay retained.
const reclaimBatch = 256

// errPayloadReclaimed means the payload a capture deduplicated onto was removed
// before the job referencing it committed. Nothing was accepted; a retry writes it again.
var errPayloadReclaimed = errors.New("sessioncapture: payload reclaimed before acceptance")

func (q *Queue) initReclaim(ctx context.Context) error {
	var existing int
	if err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='payload_refs'`).Scan(&existing); err != nil {
		return err
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Enrichment results and unfinished optional repairs hold objects too. They are
	// indexed as payload_refs holders (session column = holder key, which never
	// collides with a session BaseKey) by triggers in the writing transaction, so
	// the root check is an indexed lookup instead of a body scan.
	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS payload_hold_enrichment AFTER INSERT ON enrichment_results BEGIN INSERT OR IGNORE INTO payload_refs(sha,session) SELECT json_extract(CAST(NEW.body AS TEXT),'$.Object.encoded_sha256'),'enrichment:'||NEW.id WHERE coalesce(json_extract(CAST(NEW.body AS TEXT),'$.Object.encoded_sha256'),'')!=''; END`,
		`CREATE TRIGGER IF NOT EXISTS payload_hold_repair AFTER INSERT ON optional_repairs WHEN NEW.state!='repaired' BEGIN INSERT OR IGNORE INTO payload_refs(sha,session) SELECT json_extract(CAST(NEW.source AS TEXT),'$.object.encoded_sha256'),'repair:'||NEW.id WHERE coalesce(json_extract(CAST(NEW.source AS TEXT),'$.object.encoded_sha256'),'')!=''; END`,
		`CREATE TRIGGER IF NOT EXISTS payload_release_repair AFTER UPDATE OF state ON optional_repairs WHEN NEW.state='repaired' BEGIN DELETE FROM payload_refs WHERE session='repair:'||NEW.id; END`,
		`CREATE TABLE IF NOT EXISTS payload_reclaim (sha TEXT NOT NULL, session TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '', PRIMARY KEY(sha,session))`,
		`CREATE TABLE IF NOT EXISTS payload_refs (sha TEXT NOT NULL, session TEXT NOT NULL, PRIMARY KEY(sha,session))`,
	}
	if existing == 0 {
		// Rows accepted before this table existed are indexed once, so cross-session
		// sharing of an encoded file is always visible to the root check.
		statements = append(statements,
			`INSERT OR IGNORE INTO payload_refs(sha,session) SELECT json_extract(s.value,'$.object.encoded_sha256'),j.session FROM jobs AS j, json_each(CAST(j.manifest AS TEXT),'$.sources') AS s WHERE coalesce(json_extract(s.value,'$.object.encoded_sha256'),'')!=''`,
			`INSERT OR IGNORE INTO payload_refs(sha,session) SELECT json_extract(CAST(body AS TEXT),'$.Object.encoded_sha256'),'enrichment:'||id FROM enrichment_results WHERE coalesce(json_extract(CAST(body AS TEXT),'$.Object.encoded_sha256'),'')!=''`,
			`INSERT OR IGNORE INTO payload_refs(sha,session) SELECT json_extract(CAST(source AS TEXT),'$.object.encoded_sha256'),'repair:'||id FROM optional_repairs WHERE state!='repaired' AND coalesce(json_extract(CAST(source AS TEXT),'$.object.encoded_sha256'),'')!=''`)
	}
	for _, s := range statements {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// recordPayloadRef notes that a session references an encoded payload file.
func recordPayloadRef(ctx context.Context, db *sql.DB, o publication.Object, session string) error {
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO payload_refs(sha,session) VALUES(?,?)`, o.EncodedSHA256, session)
	return err
}

// retiredObjects lists objects old references that next no longer does.
func retiredObjects(old, next publication.Manifest) []publication.Object {
	kept := make(map[string]bool, len(next.Sources))
	for i := range next.Sources {
		kept[next.Sources[i].Object.EncodedSHA256] = true
	}
	var out []publication.Object
	for i := range old.Sources {
		// Optional artifacts are unique local bytes (possibly never published, and
		// withheld in public views while evidence still holds the original), so they
		// are never retired here.
		if old.Sources[i].Optional {
			continue
		}
		if o := old.Sources[i].Object; o.EncodedSHA256 != "" && !kept[o.EncodedSHA256] {
			out = append(out, o)
		}
	}
	return out
}

func addReclaimCandidates(ctx context.Context, tx *sql.Tx, session string, objs []publication.Object) error {
	for _, o := range objs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO payload_reclaim(sha,session) VALUES(?,?)`, o.EncodedSHA256, session); err != nil {
			return err
		}
	}
	return nil
}

// retirePublished makes the objects of the previously published job that the newly
// acknowledged job no longer references reclaimable. Remote history keeps them.
// An unreadable manifest retires nothing: keeping a payload is the safe direction.
func retirePublished(ctx context.Context, tx *sql.Tx, session string, seq uint64) error {
	var cur, prev []byte
	if err := tx.QueryRowContext(ctx, `SELECT manifest FROM jobs WHERE session=? AND sequence=?`, session, seq).Scan(&cur); err != nil {
		return err
	}
	err := tx.QueryRowContext(ctx, `SELECT manifest FROM jobs WHERE session=? AND state='published' AND sequence<? ORDER BY sequence DESC LIMIT 1`, session, seq).Scan(&prev)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return addReclaimCandidates(ctx, tx, session, retiredBetween(prev, cur))
}

// retiredBetween parses two stored manifests. An unreadable manifest retires
// nothing: keeping a payload is the safe direction, and delivery reports the
// integrity failure for such a job.
func retiredBetween(prev, cur []byte) []publication.Object {
	var m, p publication.Manifest
	if json.Unmarshal(cur, &m) != nil || json.Unmarshal(prev, &p) != nil {
		return nil
	}
	return retiredObjects(p, m)
}

// reclaimBestEffort drains pending candidates and releases the freed quota. It runs
// only after the acceptance or acknowledgement that produced the candidates has
// committed, so its failure must not be reported as that work failing. The error is
// deliberately not returned: every unfinished candidate stays a durable
// payload_reclaim row (with last_error once an attempt failed), is counted in
// RuntimeStatus.ReclaimPending/ReclaimFailed, and is retried by the next pass or Open.
func (q *Queue) reclaimBestEffort(ctx context.Context) {
	freed, _ := q.reclaimPending(ctx)
	q.mu.Lock()
	q.used -= freed
	q.mu.Unlock()
}

// reclaimPending removes the payload of every unreferenced candidate. A candidate
// that fails stays recorded with its error (visible in RuntimeStatus) and is retried
// by the next pass; only metadata failures and cancellation are returned.
func (q *Queue) reclaimPending(ctx context.Context) (freed int64, err error) {
	list, err := q.reclaimCandidates(ctx)
	if err != nil {
		return 0, err
	}
	for _, c := range list {
		n, err := q.reclaimOne(ctx, c.sha, c.session)
		freed += n
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return freed, ctx.Err()
		}
		if _, e := q.db.ExecContext(ctx, `UPDATE payload_reclaim SET last_error=? WHERE sha=? AND session=?`, err.Error(), c.sha, c.session); e != nil {
			return freed, e
		}
	}
	return freed, nil
}

func (q *Queue) reclaimOne(ctx context.Context, sha, session string) (int64, error) {
	if raw, err := hex.DecodeString(sha); err != nil || len(raw) != 32 {
		return 0, fmt.Errorf("%w: reclaim candidate", ErrIntegrity)
	}
	unlock, err := lockContext(ctx, keyedMutex(&q.installs, sha))
	if err != nil {
		return 0, err
	}
	defer unlock()
	keep, err := q.payloadKept(ctx, sha, session)
	if err != nil {
		return 0, err
	}
	if keep {
		_, err = q.db.ExecContext(ctx, `DELETE FROM payload_reclaim WHERE sha=? AND session=?`, sha, session)
		return 0, err
	}
	size, err := q.unlinkPayload(sha)
	if err != nil {
		return 0, err
	}
	if _, err := q.db.ExecContext(ctx, `DELETE FROM payload_refs WHERE sha=?`, sha); err != nil {
		return size, err
	}
	_, err = q.db.ExecContext(ctx, `DELETE FROM payload_reclaim WHERE sha=?`, sha)
	return size, err
}

// unlinkPayload removes the file (a missing file is success, so a restart after a
// crash on either side of the unlink converges) and reports the bytes freed.
func (q *Queue) unlinkPayload(sha string) (int64, error) {
	if err := q.fault("reclaim-before-unlink"); err != nil {
		return 0, err
	}
	path := filepath.Join(q.dir, "payloads", sha+".zst")
	var size int64
	q.payloadMu.Lock()
	st, err := os.Lstat(path)
	if err == nil {
		size = st.Size()
		err = os.Remove(path)
	}
	q.payloadMu.Unlock()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if err := syncDir(filepath.Join(q.dir, "payloads")); err != nil {
		return size, err
	}
	return size, q.fault("reclaim-after-unlink")
}

// payloadKept reports whether a root still needs the file. When the candidate's own
// session no longer roots it, that session's reference is dropped first, so a file
// shared by two sessions is unlinked by whichever retires it last.
func (q *Queue) payloadKept(ctx context.Context, sha, session string) (bool, error) {
	rooted, err := q.sessionRootsContain(ctx, session, sha)
	if err != nil || rooted {
		return rooted, err
	}
	if _, err := q.db.ExecContext(ctx, `DELETE FROM payload_refs WHERE sha=? AND session=?`, sha, session); err != nil {
		return false, err
	}
	var held bool
	err = q.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM payload_refs WHERE sha=?)`, sha).Scan(&held)
	return held, err
}

// sessionRootsContain reports whether an undelivered job, the latest job or the
// latest published job of the session references sha.
func (q *Queue) sessionRootsContain(ctx context.Context, session, sha string) (found bool, err error) {
	rows, err := q.db.QueryContext(ctx, `SELECT manifest FROM jobs WHERE session=?1 AND state NOT IN ('published','superseded') UNION SELECT manifest FROM (SELECT manifest FROM jobs WHERE session=?1 ORDER BY sequence DESC LIMIT 1) UNION SELECT manifest FROM (SELECT manifest FROM jobs WHERE session=?1 AND state='published' ORDER BY sequence DESC LIMIT 1)`, session)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	needle := []byte(sha)
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return false, err
		}
		if bytes.Contains(body, needle) {
			return true, nil
		}
	}
	return false, rows.Err()
}

type reclaimCandidate struct{ sha, session string }

func (q *Queue) reclaimCandidates(ctx context.Context) (list []reclaimCandidate, err error) {
	rows, err := q.db.QueryContext(ctx, `SELECT sha,session FROM payload_reclaim ORDER BY rowid LIMIT ?`, reclaimBatch)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var c reclaimCandidate
		if err := rows.Scan(&c.sha, &c.session); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}
