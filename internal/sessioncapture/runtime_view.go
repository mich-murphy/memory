package sessioncapture

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
)

// RuntimeView is a bounded, read-only snapshot. Sources, Quarantine,
// DiscoveryIssues and DiscoveryScopes have independent ID cursors; pass the
// last ID from the desired list to continue it (ignore other returned pages).
// Replay and Resume still require exclusive Queue ownership (stop the service).
type RuntimeView struct {
	Runtime         RuntimeStatus
	Sources         []SourceStatus
	Quarantine      []QuarantineEntry
	DiscoveryIssues []DiscoveryIssue
	DiscoveryScopes []DiscoveryScope
}

// ReadRuntime never acquires queue.lock, initializes tables, or resets claims.
// It can inspect an active coordinator. An uninitialized runtime returns an
// explicit database error rather than fabricating an empty healthy snapshot.
func ReadRuntime(ctx context.Context, spool, archive string, afterID int64, limit int) (RuntimeView, error) {
	var view RuntimeView
	if archive == "" {
		return view, errors.New("sessioncapture: archive required")
	}
	if err := privateInboxPath(spool, true); err != nil {
		return view, err
	}
	path := filepath.Join(spool, "queue.sqlite")
	if err := privateInboxPath(path, false); err != nil {
		return view, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return view, err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	values := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(1)"}}
	u.RawQuery = values.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return view, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "BEGIN"); err != nil {
		return view, err
	}
	// Closing the DB rolls back the read transaction, with no writes.
	var bound string
	if err := db.QueryRowContext(ctx, "SELECT archive FROM binding LIMIT 1").Scan(&bound); err != nil {
		return view, err
	}
	if bound != archive {
		return view, errors.New("sessioncapture: archive binding mismatch")
	}
	q := &Queue{db: db, opts: Options{ArchiveID: archive}}
	q.runtimeLocal().initialized = true // This read-only facade must never run InitRuntime DDL.
	if view.Runtime, err = q.RuntimeStatus(ctx); err != nil {
		return view, err
	}
	if view.Sources, err = q.WorkStatus(ctx, afterID, limit); err != nil {
		return view, err
	}
	if view.Quarantine, err = q.Quarantine(ctx, afterID, limit); err != nil {
		return view, err
	}
	if view.DiscoveryIssues, err = q.DiscoveryIssues(ctx, afterID, limit); err != nil {
		return view, err
	}
	view.DiscoveryScopes, err = q.DiscoveryScopes(ctx, afterID, limit)
	return view, err
}
