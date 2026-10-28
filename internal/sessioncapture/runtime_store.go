package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mich-murphy/memory/internal/publication"
)

// InitRuntime installs durable runtime state lazily. The Queue process flock
// guarantees that claims left by a previous process can be reset safely. Calls
// after the first initialization never reset live workers' claims.
func (q *Queue) InitRuntime(ctx context.Context) error {
	r := q.runtimeLocal()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.initialized {
		return nil
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS runtime_discovery_wakes (id INTEGER PRIMARY KEY CHECK(id=1),pending INTEGER NOT NULL DEFAULT 0,rejected INTEGER NOT NULL DEFAULT 0,truncated INTEGER NOT NULL DEFAULT 0,scanned_at INTEGER NOT NULL DEFAULT 0)`,
		`INSERT OR IGNORE INTO runtime_discovery_wakes(id) VALUES(1)`,
		`CREATE TABLE IF NOT EXISTS runtime_discovery (id INTEGER PRIMARY KEY CHECK(id=1),generation INTEGER NOT NULL DEFAULT 0,finished INTEGER NOT NULL DEFAULT 0,complete INTEGER NOT NULL DEFAULT 0,completed_at INTEGER NOT NULL DEFAULT 0,completed_generation INTEGER NOT NULL DEFAULT 0,sweep_error TEXT NOT NULL DEFAULT '',rejected_hints INTEGER NOT NULL DEFAULT 0,backpressure_hints INTEGER NOT NULL DEFAULT 0,inbox_truncated INTEGER NOT NULL DEFAULT 0)`,
		`INSERT OR IGNORE INTO runtime_discovery(id) VALUES(1)`,
		`CREATE TABLE IF NOT EXISTS runtime_discovery_issues (id INTEGER PRIMARY KEY,agent TEXT NOT NULL,root TEXT NOT NULL,path TEXT NOT NULL,code TEXT NOT NULL,first_generation INTEGER NOT NULL,seen_generation INTEGER NOT NULL,resolved_generation INTEGER NOT NULL DEFAULT 0,active INTEGER NOT NULL DEFAULT 1,UNIQUE(agent,root,path,code))`,
		`CREATE TABLE IF NOT EXISTS runtime_discovery_scopes (id INTEGER PRIMARY KEY,agent TEXT NOT NULL,root TEXT NOT NULL,state TEXT NOT NULL,examined INTEGER NOT NULL,enrichment TEXT NOT NULL,seen_generation INTEGER NOT NULL,active INTEGER NOT NULL DEFAULT 1,UNIQUE(agent,root))`,
		`CREATE TABLE IF NOT EXISTS runtime_sources (id INTEGER PRIMARY KEY,session TEXT NOT NULL,source TEXT NOT NULL,request BLOB NOT NULL,version INTEGER NOT NULL DEFAULT 1,captured_version INTEGER NOT NULL DEFAULT 0,observed_at INTEGER NOT NULL,captured_at INTEGER NOT NULL DEFAULT 0,sequence INTEGER NOT NULL DEFAULT 0,publication TEXT NOT NULL DEFAULT '',state TEXT NOT NULL DEFAULT 'pending',observed_state TEXT NOT NULL DEFAULT 'present',attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',next_retry INTEGER NOT NULL DEFAULT 0,claim INTEGER NOT NULL DEFAULT 0,UNIQUE(session,source))`,
		`CREATE INDEX IF NOT EXISTS runtime_source_claims ON runtime_sources(session,claim)`,
		`CREATE TABLE IF NOT EXISTS runtime_sessions (id INTEGER PRIMARY KEY,session TEXT UNIQUE NOT NULL,claim INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS runtime_destination (id INTEGER PRIMARY KEY CHECK(id=1),paused INTEGER NOT NULL DEFAULT 0,reason TEXT NOT NULL DEFAULT '',paused_at INTEGER NOT NULL DEFAULT 0,discovery_error TEXT NOT NULL DEFAULT '',discovered_at INTEGER NOT NULL DEFAULT 0)`,
		`INSERT OR IGNORE INTO runtime_destination(id) VALUES(1)`,
		`CREATE TABLE IF NOT EXISTS runtime_quarantine (id INTEGER PRIMARY KEY,kind TEXT NOT NULL,source_id INTEGER NOT NULL DEFAULT 0,session TEXT NOT NULL,sequence INTEGER NOT NULL DEFAULT 0,request BLOB,reason TEXT NOT NULL,attempts INTEGER NOT NULL,created_at INTEGER NOT NULL,contract TEXT NOT NULL,software_version TEXT NOT NULL DEFAULT 'unknown/devel',active INTEGER NOT NULL DEFAULT 1)`,
		`CREATE INDEX IF NOT EXISTS runtime_pending_jobs ON jobs(session,sequence) WHERE state NOT IN ('published','superseded')`,
		`CREATE TRIGGER IF NOT EXISTS runtime_job_session AFTER INSERT ON jobs BEGIN INSERT OR IGNORE INTO runtime_sessions(session) VALUES(NEW.session); END`,
		// Bootstrap only the pending index, not published archive history. All future
		// sessions are registered by the insert trigger in the acceptance transaction.
		`INSERT OR IGNORE INTO runtime_sessions(session) SELECT session FROM jobs WHERE state NOT IN ('published','superseded') GROUP BY session`,
		`UPDATE runtime_sources SET claim=0`,
		`UPDATE runtime_sessions SET claim=0`,
	}
	for _, s := range statements {
		if _, err := q.db.ExecContext(ctx, s); err != nil {
			return err
		}
		if err := q.fault("runtime-init-statement"); err != nil {
			return err
		}
	}
	for _, c := range [][2]string{{"step_errors", "INTEGER NOT NULL DEFAULT 0"}, {"last_step_error", "TEXT NOT NULL DEFAULT ''"}, {"last_step_error_at", "INTEGER NOT NULL DEFAULT 0"}} {
		if err := q.addColumnIfMissing(ctx, "runtime_destination", c[0], c[1]); err != nil {
			return err
		}
	}
	if err := q.initRuntimeSoftwareVersion(ctx); err != nil {
		return err
	}
	r.initialized = true
	return nil
}

// MaxRuntimeSources is the default retained source metadata bound, including absent sources.
// At capacity new identities receive backpressure; existing identities still
// coalesce. There is no automatic GC, nor a complete-inventory guarantee.
const MaxRuntimeSources = 4096

// MaxInventoryRecords is the hard ceiling for an explicitly configured inventory.
// It does not raise per-batch discovery or paginated status limits.
const MaxInventoryRecords = 65536

// Hint owns only bounded pathname metadata, not source bytes. Repeated hints
// coalesce by source identity; they never clear backoff or quarantine. A worker
// captures its claimed version, so completion cannot erase a newer observation.
func (q *Queue) Hint(ctx context.Context, req Request) error {
	if err := q.InitRuntime(ctx); err != nil {
		return err
	}
	if err := validateHint(req); err != nil {
		return err
	}
	req, err := q.prepareRequest(req)
	if err != nil {
		return err
	}
	if err := q.valid(req); err != nil {
		return err
	}
	for _, s := range []string{req.Identity.ArchiveID, req.Identity.MachineID, req.Identity.Agent, req.Identity.SessionID, req.SourceID, req.Path} {
		if strings.ContainsRune(s, 0) {
			return errors.New("sessioncapture: invalid hint identity")
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if len(body) > publication.MaxMetadata {
		return ErrQuota
	}
	fingerprint, err := hintFingerprint(req)
	if err != nil {
		return err
	}
	// A matching non-empty fingerprint on a present source makes the upsert a no-op
	// (no version bump, state reset or row write; RowsAffected is 0).
	result, err := q.db.ExecContext(ctx, `INSERT INTO runtime_sources(session,source,request,observed_at,fingerprint) SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM runtime_sources WHERE session=? AND source=?) OR (SELECT COUNT(*) FROM runtime_sources)<? ON CONFLICT(session,source) DO UPDATE SET request=excluded.request,version=version+1,observed_at=excluded.observed_at,fingerprint=excluded.fingerprint,observed_state='present',state=CASE WHEN state='captured' OR state='absent' THEN 'pending' ELSE state END WHERE excluded.fingerprint='' OR runtime_sources.fingerprint!=excluded.fingerprint OR runtime_sources.observed_state!='present'`, req.Identity.BaseKey(), req.SourceID, body, req.ObservedAtNS, fingerprint, req.Identity.BaseKey(), req.SourceID, q.opts.MaxInventoryRecords)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Either an unchanged existing row (fine) or the inventory cap refused a new one.
		var exists int
		if err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_sources WHERE session=? AND source=?`, req.Identity.BaseKey(), req.SourceID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrQuota
		}
	}
	// Ownership enrichment follows raw metadata admission, never under a source
	// lock or the admission statement. Its bounded ambiguity/capacity must not
	// block safe raw capture; unknown store/integrity failures remain visible.
	return q.recordHintOwnership(ctx, req)
}

// hintFingerprint binds the discovery stat digest to the normalized request
// (every field except the sweep-time ObservedAtNS). Empty stays empty.
func hintFingerprint(req Request) (string, error) {
	if req.Fingerprint == "" {
		return "", nil
	}
	req.ObservedAtNS = 0
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return digest(append([]byte(req.Fingerprint+"\x00"), body...)), nil
}

func (q *Queue) recordHintOwnership(ctx context.Context, req Request) error {
	// Manual legacy UUID hints do not claim classified ownership metadata.
	classified := false
	for _, association := range req.Associations {
		if strings.HasPrefix(association, "root:") || strings.HasPrefix(association, "adapter-session:") {
			classified = true
			break
		}
	}
	if !classified {
		return nil
	}
	err := q.RecordOwnership(ctx, req)
	if errors.Is(err, ErrOwnershipAmbiguous) || errors.Is(err, ErrOwnershipCapacity) {
		return nil
	}
	return err
}

// HintAbsent records a discovery tombstone only. Neither accepted bytes nor jobs
// are deleted. Partial discovery must not be converted into absence by callers.
func (q *Queue) HintAbsent(ctx context.Context, req Request) error {
	if err := q.Hint(ctx, req); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `UPDATE runtime_sources SET observed_state='absent',state=CASE WHEN state='quarantined' THEN state ELSE 'absent' END WHERE session=? AND source=?`, req.Identity.BaseKey(), req.SourceID)
	return err
}

type sourceWork struct {
	id, version int64
	req         Request
	attempts    int
}

func (q *Queue) claimSource(ctx context.Context) (sourceWork, error) {
	r := q.runtimeLocal()
	r.mu.Lock()
	defer r.mu.Unlock()
	var w sourceWork
	// Read at most one fixed page per turn; the cursor advances past unavailable
	// sources as well as successful work. No archive job history is inspected.
	rows, err := q.db.QueryContext(ctx, `SELECT id,request,version,attempts,claim,state,next_retry,observed_state FROM runtime_sources AS s WHERE id>? AND NOT EXISTS(SELECT 1 FROM runtime_sources AS busy WHERE busy.session=s.session AND busy.claim!=0) ORDER BY id LIMIT 64`, r.sourceCursor)
	if err != nil {
		return w, err
	}
	defer func() { _ = rows.Close() }()
	var found bool
	var visited bool
	for rows.Next() {
		var id, version, claim, next int64
		var body []byte
		var attempts int
		var state, observed string
		if err = rows.Scan(&id, &body, &version, &attempts, &claim, &state, &next, &observed); err != nil {
			break
		}
		visited = true
		r.sourceCursor = id
		if claim != 0 || state == "absent" || state == "captured" || state == "quarantined" || next > q.opts.Now().UnixNano() {
			continue
		}
		w = sourceWork{id: id, version: version, attempts: attempts}
		err = json.Unmarshal(body, &w.req)
		found = true
		break
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return sourceWork{}, err
	}
	if !found {
		if !visited {
			r.sourceCursor = 0
		}
		return sourceWork{}, nil
	}
	_, err = q.db.ExecContext(ctx, "UPDATE runtime_sources SET claim=? WHERE id=?", w.version, w.id)
	return w, err
}

func (q *Queue) captureNext(ctx context.Context, o RuntimeOptions) (did bool, resultErr error) {
	w, err := q.claimSource(ctx)
	defer func() {
		// A failed or timed-out step must not strand its claim.
		if resultErr != nil {
			q.releaseSourceClaim(ctx, w)
		}
	}()
	if err != nil || w.id == 0 {
		return false, err
	}
	m, captureErr := q.CaptureIfChanged(ctx, w.req)
	defer func() { resultErr = errors.Join(runtimeFatalCause(captureErr), resultErr) }()
	// Metadata completion gets its own bounded budget: an expired operation must
	// still release its claim and retain retry state. WithoutCancel preserves values.
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
	defer cancel()
	if captureErr == nil {
		// CaptureIfChanged may return an old manifest: observation freshness must
		// not fabricate a newer byte-ownership watermark for unchanged content.
		var capturedAt int64
		for j := range m.Sources {
			source := &m.Sources[j]
			if source.SourceID == w.req.SourceID {
				capturedAt = max(capturedAt, source.CapturedAtNS)
			}
		}
		_, err = q.db.ExecContext(save, `UPDATE runtime_sources SET claim=0,captured_version=MAX(captured_version,?),captured_at=?,sequence=?,publication=?,attempts=0,last_error='',next_retry=0,state=CASE WHEN observed_state='absent' THEN 'absent' WHEN version=? THEN 'captured' ELSE 'pending' END WHERE id=? AND claim=?`, w.version, capturedAt, m.Sequence, m.PublicationID, w.version, w.id, w.version)
		return true, err
	}
	if shutdownCancellation(ctx, captureErr) {
		// Attempts are only persisted on failure bookkeeping, so skipping it spends
		// nothing; the deferred release returns the claim for the next run.
		return true, captureErr
	}
	code := classify(captureErr, o.Classify)
	if onlySourceAbsence(captureErr) {
		code = FailureAbsent
	}
	attempts := w.attempts + 1
	state := "retry"
	if attempts >= o.MaxAttempts {
		state = "quarantined"
	}
	if code == FailureAbsent {
		state = "absent"
	}
	tx, err := q.db.BeginTx(save, nil)
	if err != nil {
		return true, err
	}
	defer func() { _ = tx.Rollback() }()
	if state == "quarantined" {
		body, e := json.Marshal(w.req)
		if e != nil {
			return true, e
		}
		_, err = tx.ExecContext(save, `INSERT INTO runtime_quarantine(kind,source_id,session,request,reason,attempts,created_at,contract,software_version) VALUES('capture',?,?,?,?,?,?,?,?)`, w.id, w.req.Identity.BaseKey(), body, string(code), attempts, q.opts.Now().UnixNano(), publication.Contract, q.runtimeSoftwareVersion())
	}
	if err == nil {
		_, err = tx.ExecContext(save, `UPDATE runtime_sources SET claim=0,attempts=?,last_error=?,next_retry=?,observed_state=CASE WHEN ?='absent' AND version=? THEN 'absent' ELSE observed_state END,state=CASE WHEN ?='absent' AND version!=? THEN 'pending' ELSE ? END WHERE id=? AND claim=?`, attempts, string(code), q.opts.Now().Add(retryDelay(attempts, o.Jitter)).UnixNano(), string(code), w.version, string(code), w.version, state, w.id, w.version)
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit()
}

func (q *Queue) destinationPaused(ctx context.Context) (bool, error) {
	var paused bool
	err := q.db.QueryRowContext(ctx, "SELECT paused FROM runtime_destination WHERE id=1").Scan(&paused)
	return paused, err
}

func (q *Queue) pauseDestination(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, "UPDATE runtime_destination SET paused=1,reason='destination',paused_at=? WHERE id=1", q.opts.Now().UnixNano())
	return err
}

// Resume explicitly clears destination pause; a restart alone never resumes it.
func (q *Queue) Resume(ctx context.Context) error {
	if err := q.InitRuntime(ctx); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, "UPDATE runtime_destination SET paused=0,reason='',paused_at=0 WHERE id=1")
	return err
}

func (q *Queue) claimSession(ctx context.Context) (int64, publication.Identity, error) {
	r := q.runtimeLocal()
	r.mu.Lock()
	defer r.mu.Unlock()
	var zero publication.Identity
	paused, err := q.destinationPaused(ctx)
	if err != nil || paused {
		return 0, zero, err
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id,session,claim FROM runtime_sessions WHERE id>? ORDER BY id LIMIT 64`, r.sessionCursor)
	if err != nil {
		return 0, zero, err
	}
	defer func() { _ = rows.Close() }()
	type candidate struct {
		id      int64
		session string
		claim   int
	}
	var page []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.session, &c.claim); err != nil {
			break
		}
		page = append(page, c)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return 0, zero, err
	}
	for _, c := range page {
		r.sessionCursor = c.id
		if c.claim != 0 {
			continue
		}
		identity, due, e := q.dueIdentity(ctx, c.session)
		if e != nil {
			return 0, zero, e
		}
		if !due {
			continue
		}
		_, err = q.db.ExecContext(ctx, "UPDATE runtime_sessions SET claim=1 WHERE id=?", c.id)
		return c.id, identity, err
	}
	if len(page) == 0 {
		r.sessionCursor = 0
	}
	return 0, zero, nil
}

func (q *Queue) deliveryNext(ctx context.Context, b publication.Backend, o RuntimeOptions) (bool, error) {
	return q.deliveryLaneNext(ctx, b, o, false)
}

func (q *Queue) deliveryLaneNext(ctx context.Context, b publication.Backend, o RuntimeOptions, optional bool) (bool, error) {
	id, identity, err := q.claimSession(ctx)
	released := false
	if id != 0 {
		defer func() {
			if !released {
				q.releaseSessionClaim(ctx, id)
			}
		}()
	}
	if err != nil || id == 0 {
		return false, err
	}
	// Claims cover scheduling only. The nonwaiting lane mutex owns delivery;
	// keeping a session claim across optional I/O would block raw scheduling.
	if _, err = q.db.ExecContext(ctx, "UPDATE runtime_sessions SET claim=0 WHERE id=?", id); err != nil {
		return false, err
	}
	released = true
	head, err := q.optionalDeliveryHead(ctx, identity)
	if err != nil {
		return false, err
	}
	if head != optional {
		return false, nil
	}
	return q.deliverDueWork(ctx, b, identity, o)
}

func (q *Queue) dueIdentity(ctx context.Context, session string) (publication.Identity, bool, error) {
	var zero publication.Identity

	var body []byte
	var state string
	var retry int64
	var seq uint64
	var attempts int
	err := q.db.QueryRowContext(ctx, `SELECT manifest,state,next_retry,sequence,attempts FROM jobs WHERE session=? AND state NOT IN ('published','superseded') ORDER BY sequence LIMIT 1`, session).Scan(&body, &state, &retry, &seq, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	if state == "quarantined" || retry > q.opts.Now().UnixNano() {
		return zero, false, nil
	}
	var m publication.Manifest
	if err := json.Unmarshal(body, &m); err != nil || publication.ValidateManifest(m) != nil || m.Identity.BaseKey() != session || m.Sequence != seq {
		return zero, false, q.quarantineMalformedJob(ctx, session, seq, attempts)
	}
	return m.Identity, true, nil
}

// Corrupt session metadata is poison for this coordinate, not the coordinator.
func (q *Queue) quarantineMalformedJob(ctx context.Context, session string, seq uint64, attempts int) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_quarantine(kind,session,sequence,reason,attempts,created_at,contract,software_version) VALUES('delivery',?,?,?,?,?,?,?)`, session, seq, string(FailureIntegrity), attempts, q.opts.Now().UnixNano(), publication.Contract, q.runtimeSoftwareVersion())
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET state='quarantined',last_error='integrity-error',next_retry=0 WHERE session=? AND sequence=?`, session, seq)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Additive upgrade of this experimental runtime table. Never replace or clear
// the authoritative queue to install a diagnostic field.
func (q *Queue) initRuntimeSoftwareVersion(ctx context.Context) error {
	if err := q.addColumnIfMissing(ctx, "runtime_sources", "fingerprint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return q.addColumnIfMissing(ctx, "runtime_quarantine", "software_version", "TEXT NOT NULL DEFAULT 'unknown/devel'")
}

// addColumnIfMissing takes only constant table/column/declaration strings.
func (q *Queue) addColumnIfMissing(ctx context.Context, table, column, decl string) error {
	rows, err := q.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var found bool
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			break
		}
		if name == column {
			found = true
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil || found {
		return err
	}
	_, err = q.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+decl)
	return err
}
