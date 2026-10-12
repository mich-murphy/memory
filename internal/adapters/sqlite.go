package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// SQLiteExports takes a consistent copy of each SQLite database under the root, through
// SQLite's backup API, and returns the copies as files to sync.
//
// Copying the file itself would miss whatever still sits in the -wal file and can catch the
// database mid-write. A copy is refreshed when the database changed and the last copy is older
// than interval, so a busy database isn't re-uploaded every sweep.
func SQLiteExports(a Adapter, root, workDir string, interval time.Duration) []SessionFile {
	var out []SessionFile
	for _, f := range Walk(a, root) {
		if !IsSQLite(filepath.Base(f.Path)) {
			continue
		}
		rel, ok := Rel(f.Path, root)
		if !ok {
			continue
		}
		dest := filepath.Join(workDir, a.Name(), RootTag(root), filepath.FromSlash(rel))
		sigPath := dest + ".sig"
		sig := dbSignature(f.Path)
		old, _ := os.ReadFile(sigPath)
		due := true
		if st, err := os.Stat(dest); err == nil {
			due = string(old) != sig && time.Since(st.ModTime()) >= interval
		}
		if due {
			if err := BackupSQLite(f.Path, dest); err != nil {
				// Locked, or not really a database: try again next sweep.
				continue
			}
			_ = os.WriteFile(sigPath, []byte(sig), 0o600) // without it the next sweep just copies again
		}
		if isFile(dest) {
			out = append(out, SessionFile{Agent: a.Name(), Path: dest, Key: KeyPrefix(a, root) + "db/" + rel, Role: "db"})
		}
	}
	return out
}

func dbSignature(db string) string {
	parts := make([]string, 0, 2)
	for _, p := range []string{db, db + "-wal"} {
		if st, err := os.Stat(p); err == nil {
			parts = append(parts, fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano()))
		} else {
			parts = append(parts, "-")
		}
	}
	return strings.Join(parts, " ")
}

type backuper interface {
	NewBackup(dstURI string) (*sqlite.Backup, error)
}

// BackupSQLite copies the database at src to dest with SQLite's online backup API, which
// includes rows still in the -wal file. dest is replaced atomically.
func BackupSQLite(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("backup %s: %w", src, err)
	}
	tmp := dest + ".partial"
	_ = os.Remove(tmp)
	u := url.URL{Scheme: "file", Path: src, RawQuery: "mode=ro&_pragma=busy_timeout(10000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fmt.Errorf("backup %s: %w", src, err)
	}
	defer func() { _ = db.Close() }() // read-only source
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("backup %s: %w", src, err)
	}
	defer func() { _ = conn.Close() }()
	err = conn.Raw(func(dc any) error {
		b, ok := dc.(backuper)
		if !ok {
			return errors.New("sqlite driver has no backup API")
		}
		bk, err := b.NewBackup(tmp)
		if err != nil {
			return err
		}
		for more := true; more; {
			if more, err = bk.Step(-1); err != nil {
				_ = bk.Finish()
				return err
			}
		}
		return bk.Finish()
	})
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup %s: %w", src, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("backup %s: %w", src, err)
	}
	return nil
}
