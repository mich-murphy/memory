// Package state is the collector's local record of what it has uploaded.
//
// The bucket is the source of truth; this database only saves work. A record is written only
// after the upload it describes has succeeded, so losing uncommitted writes in a crash means
// re-uploading a few files, never skipping one.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/gitinfo"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
)

const schema = `
CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    agent TEXT NOT NULL,
    root TEXT NOT NULL,
    key TEXT NOT NULL,
    session_key TEXT,
    role TEXT NOT NULL,
    size INTEGER NOT NULL,
    mtime_ns INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    line_count INTEGER,
    prefix_sha256 TEXT,
    head TEXT,
    deleted_at REAL
);
CREATE INDEX IF NOT EXISTS files_session ON files(agent, session_key);
CREATE INDEX IF NOT EXISTS files_key ON files(key);
-- sha256 and size are of the uncompressed bytes.
CREATE TABLE IF NOT EXISTS uploads (
    key TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL,
    size INTEGER,
    uploaded_at REAL NOT NULL
);
-- Where hashing an object's bytes left off, so an append is hashed from there (Resume).
CREATE TABLE IF NOT EXISTS resumes (
    key TEXT PRIMARY KEY,
    size INTEGER NOT NULL,
    fast INTEGER NOT NULL,
    sha_state BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS pieces (
    key TEXT NOT NULL,
    start INTEGER NOT NULL,
    end INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    object TEXT NOT NULL,
    PRIMARY KEY (key, start)
);
-- Objects that may be in the bucket without being among their file's pieces: an upload whose
-- answer never came, or a replaced segment that couldn't be deleted.
CREATE TABLE IF NOT EXISTS strays (
    key TEXT NOT NULL,
    object TEXT NOT NULL,
    PRIMARY KEY (key, object)
);
CREATE TABLE IF NOT EXISTS raw_versions (
    key TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER,
    pieces TEXT NOT NULL,
    versioned_at REAL NOT NULL,
    PRIMARY KEY (key, sha256)
);
CREATE TABLE IF NOT EXISTS sessions (
    agent TEXT NOT NULL,
    session_key TEXT NOT NULL,
    info TEXT NOT NULL,
    snapshots TEXT NOT NULL,
    launches TEXT NOT NULL DEFAULT '[]',
    first_seen REAL NOT NULL,
    PRIMARY KEY (agent, session_key)
);
-- Roots learned from hooks (CLAUDE_CONFIG_DIR, CODEX_HOME, Pi's sessionDir...), which a sweep
-- started from another agent's hook may not see.
CREATE TABLE IF NOT EXISTS roots (
    agent TEXT NOT NULL,
    path TEXT NOT NULL,
    added_at REAL NOT NULL,
    PRIMARY KEY (agent, path)
);
CREATE TABLE IF NOT EXISTS pending_manifests (
    agent TEXT NOT NULL,
    session_key TEXT NOT NULL,
    PRIMARY KEY (agent, session_key)
);
CREATE TABLE IF NOT EXISTS runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at REAL NOT NULL,
    finished_at REAL NOT NULL,
    trigger TEXT NOT NULL,
    summary TEXT NOT NULL
);
`

const runsKept = 500

func UnixSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}

func UnixNow() float64 {
	return UnixSeconds(time.Now())
}

type State struct {
	db *sql.DB
	// conn is the one connection every query runs on, so a statement is prepared once per
	// State: a sweep asks the same few questions of every file, and parsing the SQL each time
	// cost more than answering it. Transactions are BEGIN and COMMIT on it, since a sql.Tx
	// would prepare every statement again.
	conn  *sql.Conn
	inTx  bool
	ctx   context.Context
	stmts map[string]*sql.Stmt
}

func Open(ctx context.Context, dir string) (*State, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	path := filepath.Join(dir, "state.sqlite3")
	// SQLite would create it under the umask, usually world-readable.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err == nil {
		err = errors.Join(f.Chmod(0o600), f.Close())
	}
	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	// Writes record uploads that already happened, so they commit even when the run is cancelled.
	ctx = context.WithoutCancel(ctx)
	conn, err := db.Conn(ctx)
	if err == nil {
		_, err = conn.ExecContext(ctx, schema)
	}
	if err != nil {
		_ = db.Close() // already failing; this only frees the handle
		return nil, fmt.Errorf("state schema: %w", err)
	}
	return &State{db: db, conn: conn, ctx: ctx, stmts: map[string]*sql.Stmt{}}, nil
}

func (s *State) Close() error {
	errs := []error{s.Flush()}
	for _, st := range s.stmts {
		errs = append(errs, st.Close())
	}
	errs = append(errs, s.conn.Close(), s.db.Close())
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("state close: %w", err)
	}
	return nil
}

func (s *State) Flush() error {
	if !s.inTx {
		return nil
	}
	s.inTx = false
	if _, err := s.conn.ExecContext(s.ctx, "COMMIT"); err != nil {
		return fmt.Errorf("state commit: %w", err)
	}
	return nil
}

// prepare leaves query's statement in s.stmts and opens a transaction if none is open.
func (s *State) prepare(query string) error {
	if !s.inTx {
		if _, err := s.conn.ExecContext(s.ctx, "BEGIN"); err != nil {
			return fmt.Errorf("state: %w", err)
		}
		s.inTx = true
	}
	if _, ok := s.stmts[query]; ok {
		return nil
	}
	st, err := s.conn.PrepareContext(s.ctx, query) //nolint:sqlclosecheck // kept for the State's life; Close closes it
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	s.stmts[query] = st
	return nil
}

func (s *State) exec(query string, args ...any) error {
	if err := s.prepare(query); err != nil {
		return err
	}
	if _, err := s.stmts[query].ExecContext(s.ctx, args...); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	return nil
}

func (s *State) queryRow(query string, args []any, dest ...any) (bool, error) {
	if err := s.prepare(query); err != nil {
		return false, err
	}
	err := s.stmts[query].QueryRowContext(s.ctx, args...).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: %w", err)
	}
	return true, nil
}

// queryAll returns one T per row, never nil, so an empty result encodes as [].
func queryAll[T any](s *State, query string, args []any, scan func(*sql.Rows, *T) error) ([]T, error) {
	if err := s.prepare(query); err != nil {
		return nil, err
	}
	rows, err := s.stmts[query].QueryContext(s.ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor; errors surface through rows.Err
	out := []T{}
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return nil, fmt.Errorf("state: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	return out, nil
}

type File struct {
	adapters.SessionFile
	Size         int64
	MtimeNS      int64
	SHA256       string
	LineCount    int64
	PrefixSHA256 string
	Head         map[string]any
	DeletedAt    float64
}

// FileUnchanged says whether path was last recorded with this size and mtime, and how many
// pieces key's object has, in one query since a sweep asks both of every file.
func (s *State) FileUnchanged(path, key string, size, mtimeNS int64) (unchanged bool, pieces int, err error) {
	_, err = s.queryRow("SELECT size = ? AND mtime_ns = ? AND deleted_at IS NULL, (SELECT COUNT(*) FROM pieces WHERE key = ?) "+
		"FROM files WHERE path = ?", []any{size, mtimeNS, key, path}, &unchanged, &pieces)
	return unchanged, pieces, err
}

// RecordFile keeps the previous head when f.Head is nil.
func (s *State) RecordFile(f *File) error {
	var head any
	if f.Head != nil {
		b, err := jsonx.Compact(f.Head)
		if err != nil {
			return err
		}
		head = string(b)
	}
	return s.exec("INSERT OR REPLACE INTO files VALUES "+
		"(?,?,?,?,?,?,?,?,?,?,?,COALESCE(?, (SELECT head FROM files WHERE path = ?)),NULL)",
		f.Path, f.Agent, f.Root, f.Key, nullString(f.SessionKey), f.Role, f.Size, f.MtimeNS, f.SHA256,
		f.LineCount, nullString(f.PrefixSHA256), head, f.Path)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SessionFiles returns one file per key. Codex can hold one key under two paths (.jsonl then
// .jsonl.zst); the one still on disk wins.
func (s *State) SessionFiles(ref adapters.SessionRef) ([]File, error) {
	files, err := queryAll(s, "SELECT key, role, size, sha256, line_count, COALESCE(prefix_sha256, ''), "+
		"COALESCE(head, ''), COALESCE(deleted_at, 0), mtime_ns FROM files "+
		"WHERE agent = ? AND session_key = ? ORDER BY key, deleted_at IS NOT NULL", []any{ref.Agent, ref.Key},
		func(rows *sql.Rows, f *File) error {
			var head string
			err := rows.Scan(&f.Key, &f.Role, &f.Size, &f.SHA256, &f.LineCount, &f.PrefixSHA256, &head, &f.DeletedAt, &f.MtimeNS)
			f.Head, _ = jsonx.Map([]byte(head))
			return err
		})
	return slices.CompactFunc(files, func(a, b File) bool { return a.Key == b.Key }), err
}

func (s *State) LiveFiles(agent, root string) ([]adapters.SessionFile, error) {
	return queryAll(s, "SELECT path, key, COALESCE(session_key, ''), role FROM files "+
		"WHERE agent = ? AND root = ? AND deleted_at IS NULL", []any{agent, root},
		func(rows *sql.Rows, f *adapters.SessionFile) error {
			f.Agent, f.Root = agent, root
			return rows.Scan(&f.Path, &f.Key, &f.SessionKey, &f.Role)
		})
}

func (s *State) MarkDeleted(path string, at float64) error {
	return s.exec("UPDATE files SET deleted_at = ? WHERE path = ?", at, path)
}

type Upload struct {
	SHA256 string
	Size   int64
}

func (s *State) Uploaded(key string) (*Upload, error) {
	var u Upload
	found, err := s.queryRow("SELECT sha256, size FROM uploads WHERE key = ?", []any{key}, &u.SHA256, &u.Size)
	if !found || err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *State) UploadedSHA(key string) (string, error) {
	var sha string
	_, err := s.queryRow("SELECT sha256 FROM uploads WHERE key = ?", []any{key}, &sha)
	return sha, err
}

func (s *State) RecordUpload(key, sha string, size int64) error {
	return s.exec("INSERT OR REPLACE INTO uploads VALUES (?,?,?,?)", key, sha, size, UnixNow())
}

// Resume is where hashing an object's first Size bytes left off: their xxhash, and the SHA-256
// state after them. When a file's first Size bytes still have that xxhash, it only grew, and
// its SHA-256 carries on from SHA instead of reading those bytes through SHA-256 again.
type Resume struct {
	Size int64
	Fast uint64
	SHA  []byte
}

func (s *State) Resume(key string) (*Resume, error) {
	var r Resume
	var fast int64
	found, err := s.queryRow("SELECT size, fast, sha_state FROM resumes WHERE key = ?", []any{key}, &r.Size, &fast, &r.SHA)
	if !found || err != nil {
		return nil, err
	}
	r.Fast = uint64(fast)
	return &r, nil
}

func (s *State) SetResume(key string, r *Resume) error {
	return s.exec("INSERT OR REPLACE INTO resumes VALUES (?,?,?,?)", key, r.Size, int64(r.Fast), r.SHA)
}

func (s *State) Pieces(key string) ([]raw.Piece, error) {
	return queryAll(s, "SELECT start, end, sha256, object FROM pieces WHERE key = ? ORDER BY start", []any{key},
		func(rows *sql.Rows, p *raw.Piece) error { return rows.Scan(&p.Start, &p.End, &p.SHA256, &p.Object) })
}

func (s *State) SetPieces(key string, pieces []raw.Piece) error {
	if err := s.exec("DELETE FROM pieces WHERE key = ?", key); err != nil {
		return err
	}
	for _, p := range pieces {
		if err := s.AddPiece(key, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *State) AddPiece(key string, p raw.Piece) error {
	return s.exec("INSERT OR REPLACE INTO pieces VALUES (?,?,?,?,?)", key, p.Start, p.End, p.SHA256, p.Object)
}

type Stray struct {
	Key, Object string
}

func (s *State) Strays() ([]Stray, error) {
	return queryAll(s, "SELECT key, object FROM strays ORDER BY key, object", nil,
		func(rows *sql.Rows, x *Stray) error { return rows.Scan(&x.Key, &x.Object) })
}

func (s *State) AddStray(x Stray) error {
	return s.exec("INSERT OR IGNORE INTO strays VALUES (?,?)", x.Key, x.Object)
}

func (s *State) RemoveStray(x Stray) error {
	return s.exec("DELETE FROM strays WHERE key = ? AND object = ?", x.Key, x.Object)
}

func (s *State) SetStrays(key string, objects []string) error {
	if err := s.exec("DELETE FROM strays WHERE key = ?", key); err != nil {
		return err
	}
	for _, o := range objects {
		if err := s.AddStray(Stray{key, o}); err != nil {
			return err
		}
	}
	return nil
}

type Version struct {
	SHA256      string      `json:"sha256"`
	Size        int64       `json:"size"`
	VersionedAt float64     `json:"versioned_at"`
	Pieces      []raw.Piece `json:"pieces"`
}

func (s *State) RecordVersion(key string, v *Version) error {
	pieces, err := json.Marshal(v.Pieces)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	return s.exec("INSERT OR IGNORE INTO raw_versions VALUES (?,?,?,?,?)", key, v.SHA256, v.Size, string(pieces), v.VersionedAt)
}

func (s *State) Versions(key string) ([]Version, error) {
	return queryAll(s, "SELECT sha256, size, pieces, versioned_at FROM raw_versions WHERE key = ? ORDER BY versioned_at",
		[]any{key}, func(rows *sql.Rows, v *Version) error {
			var pieces string
			if err := rows.Scan(&v.SHA256, &v.Size, &pieces, &v.VersionedAt); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(pieces), &v.Pieces); err != nil {
				return fmt.Errorf("version pieces of %s: %w", key, err)
			}
			return nil
		})
}

func (s *State) LastVersionAt(key string) (float64, error) {
	var at float64
	_, err := s.queryRow("SELECT COALESCE(MAX(versioned_at), 0) FROM raw_versions WHERE key = ?", []any{key}, &at)
	return at, err
}

type Session struct {
	Info      map[string]any
	Snapshots []SnapshotEntry
	Launches  []map[string]any
	FirstSeen float64
}

type SnapshotEntry struct {
	ID        string             `json:"id"`
	MemoryID  *string            `json:"memory_id"`
	Git       gitinfo.State      `json:"git"`
	Timing    string             `json:"timing"`
	TakenAt   float64            `json:"taken_at"`
	CheckedAt float64            `json:"checked_at,omitempty"`
	Mtimes    map[string]float64 `json:"mtimes"`
}

func (s *State) Session(ref adapters.SessionRef) (*Session, error) {
	var info, snaps, launches string
	var sess Session
	found, err := s.queryRow("SELECT info, snapshots, launches, first_seen FROM sessions WHERE agent = ? AND session_key = ?",
		[]any{ref.Agent, ref.Key}, &info, &snaps, &launches, &sess.FirstSeen)
	if !found || err != nil {
		return nil, err
	}
	if err := jsonx.Decode([]byte(info), &sess.Info); err != nil {
		return nil, fmt.Errorf("state: session %s info: %w", ref.Key, err)
	}
	if err := jsonx.Decode([]byte(snaps), &sess.Snapshots); err != nil {
		return nil, fmt.Errorf("state: session %s snapshots: %w", ref.Key, err)
	}
	if err := jsonx.Decode([]byte(launches), &sess.Launches); err != nil {
		return nil, fmt.Errorf("state: session %s launches: %w", ref.Key, err)
	}
	if sess.Info == nil {
		sess.Info = map[string]any{}
	}
	return &sess, nil
}

func (s *State) PutSession(ref adapters.SessionRef, sess *Session) error {
	info, err := jsonx.Compact(orEmptyMap(sess.Info))
	if err != nil {
		return err
	}
	snaps, err := jsonx.Compact(orEmpty(sess.Snapshots))
	if err != nil {
		return err
	}
	launches, err := jsonx.Compact(orEmpty(sess.Launches))
	if err != nil {
		return err
	}
	return s.exec("INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?)", ref.Agent, ref.Key, string(info), string(snaps),
		string(launches), sess.FirstSeen)
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (s *State) MarkManifestPending(ref adapters.SessionRef) error {
	return s.exec("INSERT OR IGNORE INTO pending_manifests VALUES (?,?)", ref.Agent, ref.Key)
}

func (s *State) ManifestWritten(ref adapters.SessionRef) error {
	return s.exec("DELETE FROM pending_manifests WHERE agent = ? AND session_key = ?", ref.Agent, ref.Key)
}

func (s *State) PendingManifests() ([]adapters.SessionRef, error) {
	return queryAll(s, "SELECT agent, session_key FROM pending_manifests ORDER BY agent, session_key", nil,
		func(rows *sql.Rows, r *adapters.SessionRef) error { return rows.Scan(&r.Agent, &r.Key) })
}

func (s *State) ExtraRoots(agent string) ([]string, error) {
	return queryAll(s, "SELECT path FROM roots WHERE agent = ? ORDER BY path", []any{agent},
		func(rows *sql.Rows, p *string) error { return rows.Scan(p) })
}

func (s *State) AddExtraRoot(agent, path string) error {
	return s.exec("INSERT OR IGNORE INTO roots VALUES (?,?,?)", agent, path, UnixNow())
}

type Run struct {
	StartedAt  float64
	FinishedAt float64
	Trigger    string
	Summary    map[string]any
}

func (s *State) RecordRun(startedAt float64, trigger string, summary any) error {
	b, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	err = s.exec("INSERT INTO runs (started_at, finished_at, trigger, summary) VALUES (?,?,?,?)",
		startedAt, UnixNow(), trigger, string(b))
	if err != nil {
		return err
	}
	if err := s.exec("DELETE FROM runs WHERE id <= (SELECT MAX(id) - ? FROM runs)", runsKept); err != nil {
		return err
	}
	return s.Flush()
}

func (s *State) LastRuns(limit int) ([]Run, error) {
	return queryAll(s, "SELECT started_at, finished_at, trigger, summary FROM runs ORDER BY id DESC LIMIT ?", []any{limit},
		func(rows *sql.Rows, r *Run) error {
			var summary string
			err := rows.Scan(&r.StartedAt, &r.FinishedAt, &r.Trigger, &summary)
			r.Summary, _ = jsonx.Map([]byte(summary))
			return err
		})
}
