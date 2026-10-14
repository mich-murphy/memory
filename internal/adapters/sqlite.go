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

	"github.com/mich-murphy/memory/internal/fsutil"
)

// SQLiteExports copies each database through SQLite's backup API: copying the file itself would
// miss rows still in the -wal file and can catch the database mid-write.
func SQLiteExports(ctx context.Context, a Adapter, root string, databases []string, workDir string,
	interval time.Duration,
) []SessionFile {
	var out []SessionFile
	for _, src := range databases {
		rel, ok := relative(src, root)
		if !ok {
			continue
		}
		dest := filepath.Join(workDir, a.Name(), rootTag(root), filepath.FromSlash(rel))
		sigPath := dest + ".sig"
		sig := dbSignature(src)
		old, _ := os.ReadFile(sigPath)
		due := true
		if st, err := os.Stat(dest); err == nil {
			due = string(old) != sig && time.Since(st.ModTime()) >= interval
		}
		if due {
			if err := backupSQLite(ctx, src, dest); err != nil {
				// Locked, or not really a database: try again next sweep.
				continue
			}
			_ = os.WriteFile(sigPath, []byte(sig), 0o600) // without it the next sweep just copies again
		}
		if fsutil.IsFile(dest) {
			out = append(out, SessionFile{Agent: a.Name(), Root: root, Path: dest, Key: keyPrefix(a, root) + "db/" + rel, Role: RoleDB})
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

func backupSQLite(ctx context.Context, src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("backup %s: %w", src, err)
	}
	tmp := dest + ".partial"
	_ = os.Remove(tmp) // a leftover from a run that died; WriteFile reports a real problem
	// SQLite would create it under the umask, usually world-readable.
	if err := os.WriteFile(tmp, nil, 0o600); err != nil {
		return fmt.Errorf("backup %s: %w", src, err)
	}
	u := url.URL{Scheme: "file", Path: src, RawQuery: "mode=ro&_pragma=busy_timeout(10000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fmt.Errorf("backup %s: %w", src, err)
	}
	defer func() { _ = db.Close() }() // read-only source
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
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
