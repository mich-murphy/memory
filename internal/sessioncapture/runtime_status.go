package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type SourceStatus struct {
	ID                               int64
	Request                          Request
	ObservedVersion, CapturedVersion int64
	ObservedAtNS, CapturedAtNS       int64
	Sequence                         uint64
	PublicationID                    string
	State, ObservedState, LastError  string
	Attempts                         int
	NextRetryNS                      int64
	Claimed                          bool
}
type RuntimeStatus struct {
	// These are last-observed metadata counts, not capture/publication freshness.
	PendingDiscoveryHints, RejectedDiscoveryHints                   int64
	DiscoveryHintsTruncated                                         bool
	DiscoveryHintsScannedAtNS                                       int64
	CurrentSweep, CompletedSweep, CompletedAtNS                     int64
	DiscoveryComplete                                               bool
	ActiveDiscoveryIssues, DiscoveryIssueCount, DiscoveryScopeCount int64
	RejectedInboxHints, InboxBackpressureHints                      int64
	InboxTruncated                                                  bool
	Paused                                                          bool
	PausedReason                                                    string
	PausedAtNS                                                      int64
	DiscoveryError                                                  string
	DiscoveredAtNS                                                  int64
	Sources, PendingSources, Quarantined, PendingPublications       int64
	// RetryingPublications counts unpublished jobs that failed at least once and
	// are not quarantined; they retry indefinitely when the cause is transient.
	// OldestPendingPublicationAtNS is the capture time of the oldest unpublished
	// job (0 when none), so lag is now minus that value. MaxPendingAttempts is the
	// highest attempt count among unpublished jobs.
	RetryingPublications, MaxPendingAttempts int64
	// SpoolFullWork counts capture sources and publications currently waiting on
	// spool or free-space capacity (backpressure, not quarantine).
	SpoolFullWork int64
	// SourceLimitSources counts sources parked at the manifest source limit; a new
	// observation retries them, since an append to an existing epoch can still fit.
	SourceLimitSources int64
	// ReclaimPending counts payload files awaiting local reclamation (a committed
	// metadata decision whose unlink has not completed); ReclaimFailed is the subset
	// whose last attempt failed and will be retried.
	ReclaimPending, ReclaimFailed int64
	OldestPendingPublicationAtNS  int64
	OldestObservedAtNS            int64
	// DebouncedSources counts changed sources waiting for their quiet period (Q) or
	// maximum delay (M); DebounceLagNS is the age of the oldest such observation.
	DebouncedSources, DebounceLagNS int64
	// Discovery and enrichment remain partial; a successful timer is not proof of
	// complete source inventory. Published timestamps remain independent of capture.
	Coverage string
	// Swallowed (retried) work-step errors since the store was created. A rising
	// count with a stale CompletedAtNS indicates a persistent stall.
	StepErrors, LastStepErrorAtNS int64
	LastStepError                 string
	// Source-inventory occupancy: SourceCap bounds live work (LiveSources: pending,
	// retry, claimed, quarantined); SettledCap bounds retained settled rows.
	SourceCap, LiveSources, SettledSources, SettledCap int64
	// InventoryEvictions counts settled rows dropped from the source index at the
	// retention bound; a rising count means rediscovery re-hints them (thrash).
	InventoryEvictions int64
}
type QuarantineEntry struct {
	ID              int64
	Kind            string
	SourceID        int64
	Session         string
	Sequence        uint64
	Request         *Request
	Reason          string
	Attempts        int
	CreatedAtNS     int64
	Contract        string
	SoftwareVersion string
	Active          bool
}

func statusLimit(limit int) int {
	if limit <= 0 {
		return 64
	}
	return min(limit, 256)
}

func (q *Queue) WorkStatus(ctx context.Context, afterID int64, limit int) ([]SourceStatus, error) {
	if err := q.InitRuntime(ctx); err != nil {
		return nil, err
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id,request,version,captured_version,observed_at,captured_at,sequence,publication,state,observed_state,last_error,attempts,next_retry,claim FROM runtime_sources WHERE id>? ORDER BY id LIMIT ?`, afterID, statusLimit(limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SourceStatus{}
	for rows.Next() {
		var s SourceStatus
		var body []byte
		var claim int64
		if err := rows.Scan(&s.ID, &body, &s.ObservedVersion, &s.CapturedVersion, &s.ObservedAtNS, &s.CapturedAtNS, &s.Sequence, &s.PublicationID, &s.State, &s.ObservedState, &s.LastError, &s.Attempts, &s.NextRetryNS, &claim); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &s.Request); err != nil {
			return nil, err
		}
		s.Claimed = claim != 0
		out = append(out, s)
	}
	return out, rows.Err()
}

func (q *Queue) RuntimeStatus(ctx context.Context) (RuntimeStatus, error) {
	var s RuntimeStatus
	s.Coverage = "partial"
	if err := q.InitRuntime(ctx); err != nil {
		return s, err
	}
	if err := q.db.QueryRowContext(ctx, `SELECT pending,rejected,truncated,scanned_at FROM runtime_discovery_wakes WHERE id=1`).Scan(&s.PendingDiscoveryHints, &s.RejectedDiscoveryHints, &s.DiscoveryHintsTruncated, &s.DiscoveryHintsScannedAtNS); err != nil {
		return s, err
	}
	err := q.db.QueryRowContext(ctx, `SELECT paused,reason,paused_at,discovery_error,discovered_at,step_errors,last_step_error,last_step_error_at FROM runtime_destination WHERE id=1`).Scan(&s.Paused, &s.PausedReason, &s.PausedAtNS, &s.DiscoveryError, &s.DiscoveredAtNS, &s.StepErrors, &s.LastStepError, &s.LastStepErrorAtNS)
	if err != nil {
		return s, err
	}
	err = q.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(version>captured_version),0),COALESCE(MIN(CASE WHEN version>captured_version THEN observed_at END),0) FROM runtime_sources`).Scan(&s.Sources, &s.PendingSources, &s.OldestObservedAtNS)
	if err != nil {
		return s, err
	}
	now := q.opts.Now().UnixNano()
	if err := q.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MIN(dirty_since),0) FROM runtime_sources WHERE version>captured_version AND debounce_until>? AND dirty_since>0`, now).Scan(&s.DebouncedSources, &s.DebounceLagNS); err != nil {
		return s, err
	}
	if s.DebounceLagNS > 0 {
		s.DebounceLagNS = max(now-s.DebounceLagNS, 0)
	}
	err = q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_sources WHERE state='source-limit'`).Scan(&s.SourceLimitSources)
	if err != nil {
		return s, err
	}
	err = q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_quarantine WHERE active=1`).Scan(&s.Quarantined)
	if err != nil {
		return s, err
	}
	if err := q.discoveryStatus(ctx, &s); err != nil {
		return s, err
	}
	err = q.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(state='retry'),0),COALESCE(MAX(attempts),0),COALESCE(MIN(json_extract(CAST(manifest AS TEXT),'$.captured_at_ns')),0) FROM jobs WHERE state NOT IN ('published','superseded')`).Scan(&s.PendingPublications, &s.RetryingPublications, &s.MaxPendingAttempts, &s.OldestPendingPublicationAtNS)
	if err != nil {
		return s, err
	}
	err = q.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM runtime_sources WHERE state='retry' AND last_error='spool-full')+(SELECT COUNT(*) FROM jobs WHERE state='retry' AND last_error='spool-full')`).Scan(&s.SpoolFullWork)
	if err != nil {
		return s, err
	}
	err = q.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(last_error!=''),0) FROM payload_reclaim`).Scan(&s.ReclaimPending, &s.ReclaimFailed)
	if err != nil {
		return s, err
	}
	err = q.inventoryStatus(ctx, &s)
	return s, err
}

func (q *Queue) inventoryStatus(ctx context.Context, s *RuntimeStatus) error {
	s.SourceCap = int64(q.opts.MaxInventoryRecords)
	s.SettledCap = int64(q.settledBound())
	return q.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(state NOT IN ('captured','absent')),0),COALESCE(SUM(state IN ('captured','absent')),0),(SELECT inventory_evictions FROM runtime_destination WHERE id=1) FROM runtime_sources`).Scan(&s.LiveSources, &s.SettledSources, &s.InventoryEvictions)
}

func (q *Queue) Quarantine(ctx context.Context, afterID int64, limit int) ([]QuarantineEntry, error) {
	if err := q.InitRuntime(ctx); err != nil {
		return nil, err
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id,kind,source_id,session,sequence,request,reason,attempts,created_at,contract,software_version,active FROM runtime_quarantine WHERE id>? ORDER BY id LIMIT ?`, afterID, statusLimit(limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []QuarantineEntry{}
	for rows.Next() {
		var s QuarantineEntry
		var body []byte
		if err := rows.Scan(&s.ID, &s.Kind, &s.SourceID, &s.Session, &s.Sequence, &body, &s.Reason, &s.Attempts, &s.CreatedAtNS, &s.Contract, &s.SoftwareVersion, &s.Active); err != nil {
			return nil, err
		}
		if len(body) > 0 {
			var req Request
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, err
			}
			s.Request = &req
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Replay re-enables one quarantined coordinate, preserving all evidence and its
// audit entry. Policy and dependency checks still apply on the next attempt.
func (q *Queue) Replay(ctx context.Context, id int64) error {
	if err := q.InitRuntime(ctx); err != nil {
		return err
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var kind, session string
	var source int64
	var seq uint64
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT kind,source_id,session,sequence,active FROM runtime_quarantine WHERE id=?`, id).Scan(&kind, &source, &session, &seq, &active)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("sessioncapture: quarantine already replayed")
	}
	// Revive only work that is still quarantined. Superseded, published, captured
	// or absent work has moved on; reviving it would resurrect a stale coordinate.
	// The row is deactivated (kept, inspectable) and the caller is told via
	// ErrReplayResolved, so the resolution is neither revival nor silent disposal.
	var current string
	switch kind {
	case "capture":
		err = tx.QueryRowContext(ctx, `SELECT state FROM runtime_sources WHERE id=?`, source).Scan(&current)
	case "delivery":
		err = tx.QueryRowContext(ctx, `SELECT state FROM jobs WHERE session=? AND sequence=?`, session, seq).Scan(&current)
	default:
		return errors.New("sessioncapture: invalid quarantine kind")
	}
	if errors.Is(err, sql.ErrNoRows) {
		current, err = "missing", nil
	}
	if err != nil {
		return err
	}
	resolved := current != "quarantined"
	if !resolved {
		if kind == "capture" {
			_, err = tx.ExecContext(ctx, `UPDATE runtime_sources SET state='pending',attempts=0,last_error='',next_retry=0,observed_state='present' WHERE id=?`, source)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET state='pending',attempts=0,last_error='',next_retry=0 WHERE session=? AND sequence=?`, session, seq)
		}
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE runtime_quarantine SET active=0 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if resolved {
		return fmt.Errorf("%w: %s work is %s", ErrReplayResolved, kind, current)
	}
	return nil
}

func (q *Queue) discoveryStatus(ctx context.Context, s *RuntimeStatus) error {
	if err := q.db.QueryRowContext(ctx, `SELECT generation,completed_generation,completed_at,complete,rejected_hints,backpressure_hints,inbox_truncated FROM runtime_discovery WHERE id=1`).Scan(&s.CurrentSweep, &s.CompletedSweep, &s.CompletedAtNS, &s.DiscoveryComplete, &s.RejectedInboxHints, &s.InboxBackpressureHints, &s.InboxTruncated); err != nil {
		return err
	}
	if err := q.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(active),0) FROM runtime_discovery_issues`).Scan(&s.DiscoveryIssueCount, &s.ActiveDiscoveryIssues); err != nil {
		return err
	}
	return q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_discovery_scopes`).Scan(&s.DiscoveryScopeCount)
}
