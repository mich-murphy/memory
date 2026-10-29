package sessioncapture

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxDiscoveryRecords is the default retained issue/scope bound (including resolved
// records) and the fixed discovery batch bound. Capacity is visible backpressure, not GC or
// a claim that inventories larger than this limit have been fully captured.
const MaxDiscoveryRecords = 4096

var errDiscoveryMetadata = errors.New("sessioncapture: invalid discovery metadata")

// DiscoveryBatch describes one bounded part of a sweep, not a fresh complete
// inventory. Complete may be set only after every configured root was examined.
// Issues are safe classifications and private coordinates, never raw errors.
type DiscoveryBatch struct {
	Requests []Request
	Issues   []DiscoveryIssue
	Coverage []DiscoveryScope
	Complete bool
}

type DiscoveryIssue struct {
	ID                                                  int64
	RootAgent, RootPath, Path, Code                     string
	FirstGeneration, SeenGeneration, ResolvedGeneration int64
	Active                                              bool
}

type DiscoveryScope struct {
	ID                 int64
	Agent, Root, State string
	Examined           int
	Enrichment         string
	SeenGeneration     int64
	Active             bool
}

func discoveryToken(s string, optional bool) bool {
	if s == "" {
		return optional
	}
	if len(s) > 64 {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func discoveryPath(s string) bool {
	return len(s) <= 4096 && utf8.ValidString(s) && filepath.IsAbs(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func (q *Queue) startDiscoverySweep(ctx context.Context) error {
	if err := q.fault("runtime-sweep-before"); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `UPDATE runtime_discovery SET generation=generation+1,finished=0,complete=0,sweep_error='' WHERE id=1`)
	return err
}

// prepareDiscoverySweep advances only between batches. A restart always calls
// startDiscoverySweep independently: a lost in-process cursor cannot complete an
// old sweep, nor resolve issues observed by that cursor.
func (q *Queue) prepareDiscoverySweep(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `UPDATE runtime_discovery SET generation=generation+1,finished=0,complete=0,sweep_error='' WHERE id=1 AND (finished=1 OR generation=0)`)
	return err
}

func (q *Queue) saveDiscoveryIssue(ctx context.Context, tx *sql.Tx, issue DiscoveryIssue, generation int64) error {
	if !discoveryToken(issue.RootAgent, false) || !discoveryToken(issue.Code, false) || !discoveryPath(issue.RootPath) || !discoveryPath(issue.Path) {
		return errDiscoveryMetadata
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO runtime_discovery_issues(agent,root,path,code,first_generation,seen_generation) SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM runtime_discovery_issues WHERE agent=? AND root=? AND path=? AND code=?) OR (SELECT COUNT(*) FROM runtime_discovery_issues)<? ON CONFLICT(agent,root,path,code) DO UPDATE SET seen_generation=excluded.seen_generation,active=1`, issue.RootAgent, issue.RootPath, issue.Path, issue.Code, generation, generation, issue.RootAgent, issue.RootPath, issue.Path, issue.Code, q.opts.MaxInventoryRecords)
	return discoveryCapacity(result, err)
}

func (q *Queue) saveDiscoveryScope(ctx context.Context, tx *sql.Tx, scope DiscoveryScope, generation int64) error {
	if !discoveryToken(scope.Agent, false) || !discoveryToken(scope.State, false) || !discoveryToken(scope.Enrichment, true) || !discoveryPath(scope.Root) || scope.Examined < 0 {
		return errDiscoveryMetadata
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO runtime_discovery_scopes(agent,root,state,examined,enrichment,seen_generation) SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM runtime_discovery_scopes WHERE agent=? AND root=?) OR (SELECT COUNT(*) FROM runtime_discovery_scopes)<? ON CONFLICT(agent,root) DO UPDATE SET state=excluded.state,examined=excluded.examined,enrichment=excluded.enrichment,seen_generation=excluded.seen_generation,active=1`, scope.Agent, scope.Root, scope.State, scope.Examined, scope.Enrichment, generation, scope.Agent, scope.Root, q.opts.MaxInventoryRecords)
	return discoveryCapacity(result, err)
}

func discoveryCapacity(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrQuota
	}
	return err
}

func discoveryMetadataCode(err error) string {
	if errors.Is(err, errDiscoveryMetadata) {
		return "invalid-metadata"
	}
	return string(classify(err, nil))
}

// discoveryBatchLimit is MaxDiscoveryRecords unless a test lowered it.
func (q *Queue) discoveryBatchLimit() int {
	if q.discoveryLimit > 0 {
		return q.discoveryLimit
	}
	return MaxDiscoveryRecords
}

// saveDiscoveryBatch commits only bounded metadata. Requests are admitted before
// this transaction; no cursor, source I/O or callbacks run with a DB transaction.
// Metadata validation/capacity failures inhibit resolution for the entire sweep,
// but never discard healthy requests or terminate unrelated capture workers.
func (q *Queue) saveDiscoveryBatch(ctx context.Context, batch DiscoveryBatch, cause error) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	limit := q.discoveryBatchLimit()
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM runtime_discovery WHERE id=1`).Scan(&generation); err != nil {
		return err
	}
	code := ""
	if cause != nil {
		code = discoveryMetadataCode(cause)
	}
	if len(batch.Issues) > limit || len(batch.Coverage) > limit {
		code = "quota"
	}
	for _, issue := range batch.Issues[:min(len(batch.Issues), limit)] {
		if err := discoveryRecordError(&code, q.saveDiscoveryIssue(ctx, tx, issue, generation)); err != nil {
			return err
		}
	}
	for _, scope := range batch.Coverage[:min(len(batch.Coverage), limit)] {
		if err := discoveryRecordError(&code, q.saveDiscoveryScope(ctx, tx, scope, generation)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runtime_discovery SET sweep_error=CASE WHEN ?!='' THEN ? ELSE sweep_error END WHERE id=1`, code, code); err != nil {
		return err
	}
	if err := q.finishDiscoveryBatch(ctx, tx, generation, batch.Complete); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE runtime_destination SET discovery_error=(SELECT sweep_error FROM runtime_discovery WHERE id=1),discovered_at=? WHERE id=1`, q.opts.Now().UnixNano())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (q *Queue) finishDiscoveryBatch(ctx context.Context, tx *sql.Tx, generation int64, complete bool) error {
	if !complete {
		return nil
	}
	var code string
	if err := tx.QueryRowContext(ctx, `SELECT sweep_error FROM runtime_discovery WHERE id=1`).Scan(&code); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runtime_discovery SET finished=1,complete=?,completed_at=CASE WHEN ? THEN ? ELSE completed_at END,completed_generation=CASE WHEN ? THEN generation ELSE completed_generation END WHERE id=1`, code == "", code == "", q.opts.Now().UnixNano(), code == ""); err != nil {
		return err
	}
	if code != "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runtime_discovery_issues SET active=0,resolved_generation=? WHERE active=1 AND seen_generation!=?`, generation, generation); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE runtime_discovery_scopes SET active=0 WHERE seen_generation!=?`, generation)
	return err
}

func (q *Queue) discoverOnce(ctx context.Context, o RuntimeOptions) error {
	if err := q.prepareDiscoverySweep(ctx); err != nil {
		return err
	}
	if err := q.beginDiscoveryHints(ctx); err != nil {
		return err
	}
	limit := q.discoveryBatchLimit()
	op, cancel := context.WithTimeout(ctx, o.OperationTimeout)
	var batch DiscoveryBatch
	var err error
	if o.DiscoverBatch != nil {
		batch, err = o.DiscoverBatch(op)
	} else {
		batch.Requests, err = o.Discover(op)
	}
	// The callback may return requests after its deadline, having already advanced
	// an in-process cursor past them. Preserve its timeout before canceling; never
	// turn a timed-out Complete batch into successful coverage.
	err = errors.Join(err, op.Err())
	cancel()
	if ctx.Err() != nil {
		return nil
	}
	// Admission and metadata persistence share a separate bounded budget rooted
	// in the live parent, not the expired discovery-operation context. This does
	// not guarantee admission if this budget also expires: a subsequent complete
	// sweep (or independent hint) must revisit any unadmitted coordinates.
	save, done := context.WithTimeout(ctx, o.OperationTimeout)
	defer done()
	if len(batch.Requests) > limit {
		err = errors.Join(err, ErrQuota)
	}
	for j := range min(len(batch.Requests), limit) {
		// Try every healthy request even if another request or root was rejected.
		err = errors.Join(err, q.Hint(save, batch.Requests[j]))
	}
	if err := q.saveDiscoveryBatch(save, batch, err); err != nil {
		return err
	}
	return q.finishDiscoveryHints(save, batch.Complete)
}

func (q *Queue) DiscoveryIssues(ctx context.Context, afterID int64, limit int) ([]DiscoveryIssue, error) {
	if err := q.InitRuntime(ctx); err != nil {
		return nil, err
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id,agent,root,path,code,first_generation,seen_generation,resolved_generation,active FROM runtime_discovery_issues WHERE id>? ORDER BY id LIMIT ?`, afterID, statusLimit(limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []DiscoveryIssue{}
	for rows.Next() {
		var issue DiscoveryIssue
		if err := rows.Scan(&issue.ID, &issue.RootAgent, &issue.RootPath, &issue.Path, &issue.Code, &issue.FirstGeneration, &issue.SeenGeneration, &issue.ResolvedGeneration, &issue.Active); err != nil {
			return nil, err
		}
		out = append(out, issue)
	}
	return out, rows.Err()
}

func (q *Queue) DiscoveryScopes(ctx context.Context, afterID int64, limit int) ([]DiscoveryScope, error) {
	if err := q.InitRuntime(ctx); err != nil {
		return nil, err
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id,agent,root,state,examined,enrichment,seen_generation,active FROM runtime_discovery_scopes WHERE id>? ORDER BY id LIMIT ?`, afterID, statusLimit(limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []DiscoveryScope{}
	for rows.Next() {
		var scope DiscoveryScope
		if err := rows.Scan(&scope.ID, &scope.Agent, &scope.Root, &scope.State, &scope.Examined, &scope.Enrichment, &scope.SeenGeneration, &scope.Active); err != nil {
			return nil, err
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}

func discoveryRecordError(code *string, err error) error {
	if errors.Is(err, ErrQuota) || errors.Is(err, errDiscoveryMetadata) {
		*code = discoveryMetadataCode(err)
		return nil
	}
	return err
}
