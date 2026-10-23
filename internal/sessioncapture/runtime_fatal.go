package sessioncapture

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"syscall"

	"modernc.org/sqlite"

	"github.com/mich-murphy/memory/internal/publication"
)

// runtimeFatalCause preserves local storage failures across retry bookkeeping.
// Transport failures, SDK deadlines and integrity quarantines remain autonomous
// work outcomes. Cancellation trees are returned intact so the caller's all-leaf
// shutdown check, rather than errors.Is alone, decides whether shutdown was clean.
func runtimeFatalCause(cause error) error {
	var path *os.PathError
	var link *os.LinkError
	var db *sqlite.Error
	if errors.Is(cause, publication.ErrDurability) || errors.Is(cause, context.Canceled) ||
		errors.As(cause, &path) || errors.As(cause, &link) || errors.As(cause, &db) ||
		errors.Is(cause, sql.ErrConnDone) || errors.Is(cause, sql.ErrTxDone) || errors.Is(cause, driver.ErrBadConn) ||
		errors.Is(cause, syscall.EIO) || errors.Is(cause, syscall.ENOSPC) || errors.Is(cause, syscall.EROFS) {
		return cause
	}
	return nil
}
