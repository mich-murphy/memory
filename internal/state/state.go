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
CREATE TABLE IF NOT EXISTS pieces (
    key TEXT NOT NULL,
    start INTEGER NOT NULL,
    end INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    object TEXT NOT NULL,
    PRIMARY KEY (key, start)
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
	db  *sql.DB
	tx  *sql.Tx
	ctx context.Context
}

func Open(ctx context.Context, dir string) (*State, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	dsn := "file:" + filepath.Join(dir, "state.sqlite3") +
		"?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state schema: %w", err)
	}
	// Writes record uploads that already happened, so they commit even when the run is cancelled.
	return &State{db: db, ctx: context.WithoutCancel(ctx)}, nil
}

func (s *State) Close() error {
	ferr := s.Flush()
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("state close: %w", err)
	}
	return ferr
}

func (s *State) Flush() error {
	if s.tx == nil {
		return nil
	}
	tx := s.tx
	s.tx = nil
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state commit: %w", err)
	}
	return nil
}

func (s *State) conn() (*sql.Tx, error) {
	if s.tx == nil {
		tx, err := s.db.BeginTx(s.ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("state: %w", err)
		}
		s.tx = tx
	}
	return s.tx, nil
}

func (s *State) exec(query string, args ...any) error {
	tx, err := s.conn()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(s.ctx, query, args...); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	return nil
}

func (s *State) queryRow(query string, args []any, dest ...any) (bool, error) {
	tx, err := s.conn()
	if err != nil {
		return false, err
	}
	err = tx.QueryRowContext(s.ctx, query, args...).Scan(dest...)
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
	tx, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(s.ctx, query, args...)
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

func (s *State) FileUnchanged(path string, size, mtimeNS int64) (bool, error) {
	var one int
	return s.queryRow("SELECT 1 FROM files WHERE path = ? AND size = ? AND mtime_ns = ? AND deleted_at IS NULL",
		[]any{path, size, mtimeNS}, &one)
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
	return queryAll(s, "SELECT path, agent, root, key, COALESCE(session_key, ''), role FROM files "+
		"WHERE agent = ? AND root = ? AND deleted_at IS NULL", []any{agent, root},
		func(rows *sql.Rows, f *adapters.SessionFile) error {
			return rows.Scan(&f.Path, &f.Agent, &f.Root, &f.Key, &f.SessionKey, &f.Role)
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
	Git       GitState           `json:"git"`
	Timing    string             `json:"timing"`
	TakenAt   float64            `json:"taken_at"`
	CheckedAt float64            `json:"checked_at,omitempty"`
	Mtimes    map[string]float64 `json:"mtimes"`
}

type GitState struct {
	Root      string `json:"root,omitempty"`
	CommonDir string `json:"common_dir,omitempty"`
	Remote    string `json:"remote,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Head      string `json:"head,omitempty"`
	Dirty     *bool  `json:"dirty,omitempty"`
	RepoID    string `json:"repo_id,omitempty"`
}

func (g GitState) IsZero() bool {
	return g == GitState{}
}

func (s *State) GetSession(ref adapters.SessionRef) (*Session, error) {
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
