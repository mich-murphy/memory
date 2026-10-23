package adapters

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"modernc.org/sqlite"
)

// Shared output bounds remain mandatory for the authorized SQLite source VFS.
// Source authorization and WAL observation live in sqlite_authorized.go; there
// is no candidate entry point that reopens a source by ordinary pathname.
func stepBoundedBackup(ctx context.Context, bk *sqlite.Backup, dest string, limit int64, check func() error) error {
	for {
		err := checkBackupStep(ctx, dest, limit, check)
		if err != nil {
			_ = bk.Finish()
			return err
		}
		more, err := bk.Step(1)
		if err != nil {
			_ = bk.Finish()
			return err
		}
		if !more {
			return bk.Finish()
		}
	}
}

func checkBackupStep(ctx context.Context, dest string, limit int64, check func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	st, err := os.Stat(dest)
	if err != nil {
		return err
	}
	if st.Size() > limit-65536 {
		return errors.New("sqlite backup working limit exceeded")
	}
	return nil
}

func boundedSQLiteDimensions(ctx context.Context, tx *sql.Tx, limit int64) error {
	var pages, size int64
	if err := tx.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return err
	}
	if pages < 0 || size <= 0 || size > 65536 || pages > limit/size {
		return errors.New("sqlite backup working limit exceeded")
	}
	return nil
}
