package adapters

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
	"modernc.org/sqlite/vfs"
)

// proofPinnedFS has no host pathname fallback, SHM, journal, or write methods.
// Each SQLite handle owns its cursor, not the descriptor lifetime.
type proofPinnedFS map[string]*os.File

func (p proofPinnedFS) Open(name string) (fs.File, error) {
	f, ok := p[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &proofPinnedFile{SectionReader: io.NewSectionReader(f, 0, st.Size()), info: st}, nil
}

type proofPinnedFile struct {
	*io.SectionReader
	info fs.FileInfo
}

func (p *proofPinnedFile) Stat() (fs.FileInfo, error) { return p.info, nil }
func (p *proofPinnedFile) Close() error               { return nil }

func TestAuthorizedSQLiteImmutableWALProofRejectsMissingRows(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	producer, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = producer.Close() }()
	producer.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE evidence (v INTEGER)", "INSERT INTO evidence VALUES (41), (42), (43)"} {
		if _, err := producer.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	pins := proofPinnedFS{}
	for _, suffix := range []string{"", "-wal"} {
		f, err := os.Open(source + suffix)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		pins["main.db"+suffix] = f
	}
	st, err := pins["main.db-wal"].Stat()
	if err != nil || st.Size() == 0 {
		t.Fatal("fixture lacks WAL", err)
	}
	name, registered, err := vfs.New(pins)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := registered.Close(); err != nil {
			t.Error(err)
		}
	}()
	db, err := sql.Open("sqlite", "file:main.db?immutable=1&mode=ro&vfs="+name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	dest := filepath.Join(dir, "prepared.db")
	if err := os.WriteFile(dest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := conn.Raw(func(dc any) error {
		bk, err := dc.(backuper).NewBackup(dest)
		if err != nil {
			return err
		}
		return stepBoundedBackup(t.Context(), bk, dest, 1<<30, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	out, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	var count, sum int
	// This is a negative capability proof, not an accepted backup implementation.
	// A successful backup call is insufficient: this VFS/URI silently loses WAL.
	err = out.QueryRowContext(t.Context(), "SELECT count(*),sum(v) FROM evidence").Scan(&count, &sum)
	if err == nil || !strings.Contains(err.Error(), "no such table: evidence") {
		t.Fatalf("expected demonstrated missing-WAL schema; count=%d sum=%d err=%v", count, sum, err)
	}
}

func TestAuthorizedSQLiteReadonlyVFSWALProof(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	producer, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = producer.Close() }()
	producer.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE evidence (v INTEGER)", "INSERT INTO evidence VALUES (41), (42), (43)"} {
		if _, err := producer.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	pins := proofPinnedFS{}
	for _, suffix := range []string{"", "-wal"} {
		f, err := os.Open(source + suffix)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		pins["main.db"+suffix] = f
	}
	st, err := pins["main.db-wal"].Stat()
	if err != nil || st.Size() == 0 {
		t.Fatal("fixture lacks WAL", err)
	}
	name, registered, err := newAuthorizedVFS(pins)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := registered.Close(); err != nil {
			t.Error(err)
		}
	}()
	db, err := sql.Open("sqlite", "file:main.db?mode=ro&_pragma=locking_mode(EXCLUSIVE)&vfs="+name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	dest := filepath.Join(dir, "prepared.db")
	if err := os.WriteFile(dest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := conn.Raw(func(dc any) error {
		bk, err := dc.(backuper).NewBackup(dest)
		if err != nil {
			return err
		}
		return stepBoundedBackup(t.Context(), bk, dest, 1<<30, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	out, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	var count, sum int
	if err := out.QueryRowContext(t.Context(), "SELECT count(*),sum(v) FROM evidence").Scan(&count, &sum); err != nil {
		t.Fatal(err)
	}
	if count != 3 || sum != 126 {
		t.Fatal("missing WAL rows", count, sum)
	}
}

func TestAuthorizedSQLiteAPI(t *testing.T) {
	for _, scenario := range []string{"stable", "deny-wal", "replace-main", "mutate-wal", "cancel", "appear-wal", "rollover", "ancestor-alias", "leaf-alias"} {
		t.Run(scenario, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(dir, "source.db")
			producer, err := sql.Open("sqlite", source)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = producer.Close() }()
			producer.SetMaxOpenConns(1)
			queries := []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE evidence(v INTEGER)", "INSERT INTO evidence VALUES(41),(42),(43)"}
			if scenario == "appear-wal" {
				queries = []string{"CREATE TABLE evidence(v INTEGER)"}
			}
			for _, q := range queries {
				if _, err := producer.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(dir, "prepared.db")
			if err := os.WriteFile(dest, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			before := make(map[string][]byte)
			if scenario == "stable" {
				for _, suffix := range []string{"", "-wal", "-shm"} {
					b, e := os.ReadFile(source + suffix)
					if e != nil {
						t.Fatal(e)
					}
					before[suffix] = b
				}
			}
			called := 0
			changed := false
			err = BackupSQLiteAuthorized(ctx, source, dest, 1<<30, func() error {
				if !changed {
					changed = true
					if scenario == "rollover" {
						if e := os.Rename(source+"-wal", source+"-wal.old"); e != nil {
							return e
						}
						return os.WriteFile(source+"-wal", nil, 0o600)
					}
					if scenario == "mutate-wal" {
						_, e := producer.ExecContext(ctx, "INSERT INTO evidence VALUES(99)")
						return e
					}
					if scenario == "appear-wal" {
						return os.WriteFile(source+"-wal", []byte("not-a-wal"), 0o600)
					}
				}
				return nil
			}, SQLiteAuthorizedOptions{Authorize: func(_ context.Context, _ SQLiteAuthorization) error {
				called++
				if called == 2 {
					switch scenario {
					case "deny-wal":
						return errors.New("denied")
					case "cancel":
						cancel()
					case "ancestor-alias":
						if e := os.Rename(dir, dir+".old"); e != nil {
							return e
						}
						t.Cleanup(func() { _ = os.Remove(dir); _ = os.Rename(dir+".old", dir) })
						return os.Symlink(dir+".old", dir)
					case "leaf-alias":
						if e := os.Rename(source, source+".old"); e != nil {
							return e
						}
						return os.Symlink(source+".old", source)
					case "replace-main":
						return os.Rename(source, source+".old")
					}
				}
				return nil
			}})
			if scenario != "stable" {
				if err == nil {
					t.Fatal("unsafe observation accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for suffix, b := range before {
				after, e := os.ReadFile(source + suffix)
				if e != nil || !bytes.Equal(b, after) {
					t.Fatal("original bytes modified", suffix, e)
				}
			}
			if called != 2 {
				t.Fatal("missing sidecar authorization")
			}
			out, e := sql.Open("sqlite", dest)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = out.Close() }()
			var count, sum int
			if err := out.QueryRowContext(ctx, "SELECT count(*),sum(v) FROM evidence").Scan(&count, &sum); err != nil {
				t.Fatal(err)
			}
			if count != 3 || sum != 126 {
				t.Fatal("WAL rows lost")
			}
		})
	}
}

func TestAuthorizedSQLiteRejectsJournalBeforeAuthorization(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "source.db")
	if err := os.WriteFile(src, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src+"-journal", []byte("hot"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	if err := os.WriteFile(dest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err = BackupSQLiteAuthorized(t.Context(), src, dest, 1<<30, func() error { return nil }, SQLiteAuthorizedOptions{Authorize: func(context.Context, SQLiteAuthorization) error { called = true; return nil }})
	if !errors.Is(err, ErrSQLiteRollbackJournal) || called {
		t.Fatalf("journal must reject with its typed cause before authorization or body reads: %v, callback=%v", err, called)
	}
}

func TestAuthorizedSQLiteObservationEdges(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "db")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observeSQLitePair(src, expected); err == nil {
		t.Fatal("old caller identity ignored")
	}
	if _, err := observeSQLitePair(src+"missing", nil); err == nil {
		t.Fatal("missing main accepted")
	}
	if err := os.Mkdir(src+"-wal", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := observeSQLitePair(src, nil); err == nil {
		t.Fatal("nonregular WAL accepted")
	}
	if err := os.Remove(src + "-wal"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src+"missing", src+"-wal"); err != nil {
		t.Fatal(err)
	}
	if _, err := observeSQLitePair(src, nil); err == nil {
		t.Fatal("dangling WAL accepted")
	}
	if err := os.Remove(src + "-wal"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	pair, err := observeSQLitePair(alias, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pair[0].meta.ResolvedPath != src || !pair[1].meta.Present {
		t.Fatal("alias lost physical WAL")
	}
}

func TestAuthorizedSQLiteRetainsSidecarEvidenceBeforeRead(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "db")
	if err := os.WriteFile(src, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	if err := os.WriteFile(dest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	authorized, observed := 0, 0
	stop := errors.New("retention budget full")
	err = BackupSQLiteAuthorized(t.Context(), src, dest, 1<<30, func() error { t.Fatal("backup reached before evidence retention"); return nil }, SQLiteAuthorizedOptions{
		Authorize: func(context.Context, SQLiteAuthorization) error { authorized++; return nil },
		ObserveInput: func(m SQLiteAuthorization) error {
			if authorized != 2 {
				t.Fatal("retention before all authorizations")
			}
			observed++
			if observed == 2 {
				if m.OriginalPath != src+"-wal" || m.Present || len(m.Identities) == 0 {
					t.Fatal("missing absent sidecar evidence")
				}
				return stop
			}
			return nil
		},
	})
	if !errors.Is(err, stop) || observed != 2 {
		t.Fatal("retention failure ignored", err)
	}
}

func TestAuthorizedSQLiteClosedNamespaceAndWrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	seed, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.ExecContext(t.Context(), "CREATE TABLE t(v INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	pins := sqlitePinnedFS{ctx: t.Context(), files: map[string]*os.File{"main.db": f}}
	for _, p := range []string{src, "../main.db", "main.db-shm", "main.db-journal", "other.db"} {
		if h, err := pins.Open(p); err == nil {
			_ = h.Close()
			t.Fatal("namespace escape", p)
		}
	}
	name, registered, err := newAuthorizedVFS(pins)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := registered.Close(); err != nil {
			t.Error(err)
		}
	}()
	db, err := sql.Open("sqlite", "file:main.db?mode=ro&_pragma=locking_mode(EXCLUSIVE)&vfs="+name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := db.ExecContext(t.Context(), "INSERT INTO t VALUES(7)"); err == nil {
		t.Fatal("source write permitted")
	}
	if _, err := db.ExecContext(t.Context(), "ATTACH 'other.db' AS outside"); err == nil {
		t.Fatal("source creation permitted")
	}
	// Defensive callbacks cannot accidentally become writes even if reached by a
	// future SQLite error/cleanup path. They never dereference their arguments.
	if authorizedVFSWrite(nil, 0, 0, 0, 0) != sqlite3.SQLITE_READONLY || authorizedVFSTruncate(nil, 0, 0) != sqlite3.SQLITE_READONLY || authorizedVFSSync(nil, 0, 0) != sqlite3.SQLITE_READONLY || authorizedVFSDelete(nil, 0, 0, 0) != sqlite3.SQLITE_READONLY {
		t.Fatal("readonly callback contract")
	}
}
