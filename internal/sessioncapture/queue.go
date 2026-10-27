// Package sessioncapture owns accepted offline bytes, not disposable staging.
// SQLite (WAL, synchronous FULL) is authoritative metadata; private compressed files
// are fsynced before a short acceptance transaction. A process lock fences other
// openers. No metadata transaction spans source I/O or network operations.
//
// All payloads, sealed epochs, intermediate accepted publications and crash orphans
// are retained: there is deliberately no GC. MaxSpoolBytes bounds compressed payload
// bytes (including orphans), not SQLite metadata or filesystem overhead. Operators
// must budget those separately. Source snapshots are double-hashed with stat checks;
// this detects ordinary mutation, not producer atomicity or hostile filesystem races.
// Raw transcript/artifact roles and explicit bounded derived transforms are supported;
// protected configuration must pass internal scrubbing, never a raw fallback.
// PreparedWorkingBytes is a separate working-disk bound. Immutable publication time is
// zero (unknown), not a
// capture timestamp presented as transport completion. Published status is only
// committed locally after authoritative remote confirmation.
// Queue directories and their ancestors must be owned/trusted; hostile concurrent
// filesystem replacement is not supported. Metadata and quota use are unbounded
// in time because retention is intentional; quota exhaustion rejects new bytes.
// Capture serializes within a session only. Compressed writes reserve quota before
// I/O, including duplicate temporary peaks. NextRetryNS is advisory:
// an explicit Deliver call retries immediately, permitting operator replay.
// Deliver is an immediate bounded single pass over pending jobs. Opt-in Run owns
// autonomous bounded discovery/capture/delivery retries until its context is canceled;
// opening a Queue alone starts no daemon.
package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite" // Register the authoritative queue database driver.

	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/scrub"
)

var (
	ErrQuota           = errors.New("sessioncapture: spool quota exceeded")
	ErrIntegrity       = errors.New("sessioncapture: payload integrity failure")
	ErrSessionExcluded = errors.New("sessioncapture: session excluded")
	ErrPolicy          = errors.New("sessioncapture: policy denied")
	// ErrQueueOpen reports that another process holds the single-writer queue lock.
	ErrQueueOpen = errors.New("sessioncapture: queue already open")
)

type Options struct {
	Coordinates      *capturecoordinates.Inventory
	EnableEnrichment bool
	ArchiveID        string
	MaxSpoolBytes    int64
	// MaxInventoryRecords bounds each retained runtime source/issue/scope table.
	// Zero preserves the default 4096; positive values may not exceed 65536.
	MaxInventoryRecords int
	// MinFreeBytes reserves filesystem space; zero disables this additional gate.
	MinFreeBytes int64
	CheckPolicy  func(context.Context, Request) error
	Now          func() time.Time
	Fault        func(string) error
}
type Request struct {
	Identity             publication.Identity
	SourceID, Role, Path string
	LogicalPath          string   `json:",omitempty"`
	Identities           []string `json:",omitempty"`
	Optional             bool     `json:",omitempty"`
	Transform            string   `json:",omitempty"`
	ObservedAtNS         int64
	Associations         []string
}
type Status struct {
	Identity             publication.Identity
	Sequence             uint64
	PublicationID, State string
	OptionalRepairGap    string
	EnrichmentGap        string
	Attempts             int
	LastError            string
	NextRetryNS          int64
}
type evidence struct {
	SelectionReason string              `json:",omitempty"`
	ResultID        string              `json:",omitempty"`
	Original        *publication.Source `json:",omitempty"`
	Assets          []EnrichmentAsset   `json:",omitempty"`
	Spec            *EnrichmentSpec     `json:",omitempty"`
	Request         Request
	Identities      []string
}
type Queue struct {
	db         *sql.DB
	dir        string
	opts       Options
	lock       *flock.Flock
	policy     *capturepolicy.Policy
	mu         sync.Mutex   // protects quota counters only; never held across source I/O
	payloadMu  sync.RWMutex // fences directory scans against internal rename/remove only
	used       int64
	writing    int64
	deliveries sync.Map
	sessions   sync.Map
	installs   sync.Map
}

func Open(ctx context.Context, dir string, opts Options) (*Queue, error) {
	if opts.ArchiveID == "" || opts.MaxSpoolBytes <= 0 || opts.MinFreeBytes < 0 || opts.CheckPolicy == nil {
		return nil, errors.New("sessioncapture: archive, positive quota and explicit policy required")
	}
	limit, err := inventoryRecordLimit(opts.MaxInventoryRecords)
	if err != nil {
		return nil, err
	}
	opts.MaxInventoryRecords = limit
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Join(dir, "payloads"), 0o700); err != nil {
		return nil, err
	}
	if err := privateDirs(dir); err != nil {
		return nil, err
	}
	l := flock.New(filepath.Join(dir, "queue.lock"))
	ok, err := l.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrQueueOpen
	}
	dbPath := filepath.Join(dir, "queue.sqlite")
	if st, e := os.Lstat(dbPath); e == nil && (!st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0) {
		_ = l.Unlock()
		return nil, errors.New("sessioncapture: unsafe database file")
	}
	file, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = l.Unlock()
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = l.Unlock()
		return nil, err
	}
	// busy_timeout first, per connection: journal_mode=WAL and the schema
	// statements take locks that concurrent read-only inspectors may hold.
	dsn := url.URL{Scheme: "file", Path: dbPath, RawQuery: url.Values{"_pragma": {"busy_timeout(5000)", "synchronous(FULL)"}}.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		_ = l.Unlock()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Queue, error) { _ = db.Close(); _ = l.Unlock(); return nil, e }
	if err = initializeDB(ctx, db, dir, opts.ArchiveID); err != nil {
		return fail(err)
	}
	p, err := capturepolicy.New("", dir)
	if err != nil {
		return fail(err)
	}
	q := &Queue{db: db, dir: dir, opts: opts, lock: l, policy: p}
	if err := q.initEnrichment(ctx); err != nil {
		return fail(err)
	}
	used, err := q.usage()
	if err != nil {
		return fail(err)
	}
	if used > opts.MaxSpoolBytes {
		return fail(ErrQuota)
	}
	q.used = used
	if err := syncQueueTree(dir); err != nil {
		return fail(err)
	}
	return q, nil
}

// MkdirAll may have created more than the immediate queue parent. Fence every
// directory entry before accepting bytes, including retry after an earlier sync
// error left a newly created ancestor visible but not durably acknowledged.
func syncQueueTree(dir string) error {
	path, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for {
		if err := syncDir(path); err != nil {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func (q *Queue) Close() error { return errors.Join(q.db.Close(), q.lock.Unlock()) }
func (q *Queue) session(i publication.Identity) *sync.Mutex {
	return keyedMutex(&q.sessions, i.BaseKey())
}

func keyedMutex(locks *sync.Map, key string) *sync.Mutex {
	v, _ := locks.LoadOrStore(key, new(sync.Mutex))
	lock, ok := v.(*sync.Mutex)
	if !ok {
		panic("sessioncapture: invalid internal lock")
	}
	return lock
}

// lockSession preserves session ordering while allowing canceled workers to stop
// waiting behind another capture or a slow delivery. No waiter goroutine is used.
func (q *Queue) lockSession(ctx context.Context, i publication.Identity) (func(), error) {
	return lockContext(ctx, q.session(i))
}

func lockContext(ctx context.Context, lock *sync.Mutex) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if lock.TryLock() {
			return lock.Unlock, nil
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (q *Queue) fault(s string) error {
	if q.opts.Fault != nil {
		return q.opts.Fault(s)
	}
	return nil
}

func (q *Queue) valid(r Request) error {
	if err := validateRequestProvenance(r); err != nil {
		return err
	}
	if r.Optional && r.Role != "artifact" {
		return ErrPolicy
	}
	if !slices.Contains([]string{"", "zstd", "sqlite-backup", "scrubbed-config"}, r.Transform) {
		return ErrPolicy
	}
	if r.Identity.ArchiveID != q.opts.ArchiveID || r.Identity.MachineID == "" || r.Identity.Agent == "" || r.Identity.SessionID == "" || r.SourceID == "" || r.Path == "" {
		return errors.New("sessioncapture: incomplete identity")
	}
	if !slices.Contains([]string{"main", "subagent", "tool-result", "transcript", "artifact"}, r.Role) {
		return fmt.Errorf("%w: only raw conversation/artifact roles supported", ErrPolicy)
	}
	return nil
}

func (q *Queue) check(ctx context.Context, e evidence, delivery bool) (capturepolicy.Resolution, error) {
	r, err := q.checkObserved(ctx, e, delivery)
	return r.Resolution, err
}

func (q *Queue) checkObserved(ctx context.Context, e evidence, delivery bool) (authorizedSource, error) {
	if err := q.checkEvidenceAssets(ctx, e, delivery); err != nil {
		return authorizedSource{}, err
	}
	if err := q.valid(e.Request); err != nil {
		return authorizedSource{}, err
	}
	_, declared, coordinateErr := q.requestCoordinates(e.Request)
	if coordinateErr != nil {
		return authorizedSource{}, coordinateErr
	}
	r, err := capturepolicy.Resolve(e.Request.Path)
	observed := authorizedSource{Resolution: r}
	// Bind byte authority before invoking even the first caller callback.
	if err == nil && !delivery {
		observed.before, err = os.Lstat(r.Path)
	}

	ids := unionIdentities(unionIdentities(e.Identities, declared), r.Identities)
	if err := boundPolicyIdentities(ids); err != nil {
		return observed, err
	}
	observed.Identities = ids
	if callbackErr := q.checkCallback(ctx, e.Request); callbackErr != nil {
		return observed, callbackErr
	}
	excluded := q.policy.Excludes(ids)
	if e.Request.Transform == "scrubbed-config" {
		excluded = q.policy.ExcludesClaudeConfig(ids)
	}
	if excluded {
		return observed, ErrPolicy
	}
	recognized := slices.ContainsFunc(ids, scrub.Recognizes)
	if recognized != (e.Request.Transform == "scrubbed-config") {
		return observed, ErrPolicy
	}
	// Every historical resolution spelling is also checked by the caller's policy.
	for _, p := range ids {
		req := e.Request
		req.Path = p
		if err := q.checkCallback(ctx, req); err != nil {
			return observed, err
		}
	}
	if err != nil && (!delivery || !errors.Is(err, os.ErrNotExist)) {
		return observed, err
	}
	return observed, nil
}

func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}

func (q *Queue) latest(ctx context.Context, i publication.Identity) (publication.Manifest, []evidence, error) {
	var m publication.Manifest
	var es []evidence
	var b, e []byte
	err := q.db.QueryRowContext(ctx, "SELECT manifest,evidence FROM jobs WHERE session=? ORDER BY sequence DESC LIMIT 1", i.BaseKey()).Scan(&b, &e)
	if errors.Is(err, sql.ErrNoRows) {
		return m, es, nil
	}
	if err != nil {
		return m, es, err
	}
	if err = json.Unmarshal(b, &m); err == nil {
		err = json.Unmarshal(e, &es)
	}
	return m, es, err
}

func (q *Queue) Status(ctx context.Context) ([]Status, error) {
	rows, err := q.db.QueryContext(ctx, "SELECT manifest,state,attempts,last_error,next_retry,enrichment_gap,optional_repair_gap FROM jobs ORDER BY session,sequence")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // Read-only query; rows.Err checked below.
	var out []Status
	for rows.Next() {
		var b []byte
		var s Status
		if err := rows.Scan(&b, &s.State, &s.Attempts, &s.LastError, &s.NextRetryNS, &s.EnrichmentGap, &s.OptionalRepairGap); err != nil {
			return nil, err
		}
		var m publication.Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		s.Identity = m.Identity
		s.Sequence = m.Sequence
		s.PublicationID = m.PublicationID
		out = append(out, s)
	}
	return out, rows.Err()
}

func initializeDB(ctx context.Context, db *sql.DB, dir, archiveID string) error {
	var err error
	for _, s := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", `CREATE TABLE IF NOT EXISTS binding (archive TEXT NOT NULL)`, `CREATE TABLE IF NOT EXISTS jobs (session TEXT NOT NULL, sequence INTEGER NOT NULL, manifest BLOB NOT NULL, evidence BLOB NOT NULL, state TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', next_retry INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(session,sequence))`} {
		if _, err = db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	if err := os.Chmod(filepath.Join(dir, "queue.sqlite"), 0o600); err != nil {
		return err
	}
	var archive string
	err = db.QueryRowContext(ctx, "SELECT archive FROM binding LIMIT 1").Scan(&archive)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = db.ExecContext(ctx, "INSERT INTO binding VALUES (?)", archiveID)
		archive = archiveID
	}
	if err != nil {
		return err
	}
	if archive != archiveID {
		return errors.New("sessioncapture: archive binding mismatch")
	}
	return nil
}

func privateDirs(dir string) error {
	for _, p := range []string{dir, filepath.Join(dir, "payloads")} {
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0o077 != 0 {
			return errors.New("sessioncapture: private real directories required")
		}
	}

	return nil
}

func (q *Queue) checkEvidenceAssets(ctx context.Context, e evidence, delivery bool) error {
	if e.ResultID != "" {
		r, err := q.loadEnrichmentResult(ctx, e.ResultID)
		if err != nil {
			return err
		}
		e.Spec = &r.Spec
		e.Assets = r.Assets
	}
	if e.Spec == nil {
		return nil
	}
	options := q.runtimeLocal().enrichmentOptions.Load()
	if options == nil {
		if len(e.Assets) > 0 {
			return ErrPolicy
		}
		return nil
	}
	return checkAssets(ctx, *e.Spec, e.Assets, delivery, *options)
}

func (q *Queue) checkCallback(ctx context.Context, r Request) error {
	err := q.opts.CheckPolicy(ctx, r)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrSessionExcluded) {
		return errors.Join(ErrPolicy, ErrSessionExcluded)
	}
	if ctx.Err() != nil && pureCancellation(err, ctx.Err()) {
		return ctx.Err()
	}
	return fmt.Errorf("%w: callback denied", ErrPolicy)
}

// inventoryRecordLimit normalizes the optional operating quota before any disk I/O.
func inventoryRecordLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxInventoryRecords {
		return 0, errors.New("sessioncapture: inventory quota out of bounds")
	}
	if limit == 0 {
		return MaxRuntimeSources, nil
	}
	return limit, nil
}
