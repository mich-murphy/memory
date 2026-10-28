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

// ErrPolicyUnavailable marks a sanitized policy-store authority failure. Callers
// must not use it for ordinary policy denial or expose private store diagnostics.
var ErrPolicyUnavailable = errors.New("sessioncapture: policy authority unavailable")

// runtimeFatalCause preserves local storage failures across retry bookkeeping.
// Transport failures, SDK deadlines and integrity quarantines remain autonomous
// work outcomes. Cancellation trees are returned intact so the caller's all-leaf
// shutdown check, rather than errors.Is alone, decides whether shutdown was clean.
func runtimeFatalCause(cause error) error {
	var path *os.PathError
	var link *os.LinkError
	var policy *policyStorageFailure
	if errors.As(cause, &policy) || errors.Is(cause, ErrPolicyUnavailable) || errors.Is(cause, publication.ErrDurability) || errors.Is(cause, context.Canceled) ||
		errors.As(cause, &path) || errors.As(cause, &link) || fatalSQLite(cause) ||
		errors.Is(cause, sql.ErrConnDone) || errors.Is(cause, sql.ErrTxDone) || errors.Is(cause, driver.ErrBadConn) ||
		errors.Is(cause, syscall.ENOENT) || errors.Is(cause, syscall.EIO) || errors.Is(cause, syscall.ENOSPC) || errors.Is(cause, syscall.EROFS) {
		return cause
	}
	return nil
}

// fatalSQLite is true when any leaf is a sqlite error other than SQLITE_INTERRUPT,
// which only reports that an expired operation context cancelled a running query.
func fatalSQLite(cause error) bool {
	return !leavesAre(cause, func(leaf error) bool {
		var se *sqlite.Error
		return !errors.As(leaf, &se) || se.Code()&0xff == 9
	})
}

// sourceAbsence is created only at an authorized original-source read boundary,
// never around callbacks, prepared output, retained payloads or queue metadata.
// Deliberately do not unwrap the PathError: the scheduler must not confuse this
// expected source outcome with a fatal local-store PathError. The private path
// is not needed for the durable failure code either.
type sourceAbsence struct{}

func (*sourceAbsence) Error() string        { return "sessioncapture: source absent" }
func (*sourceAbsence) Is(target error) bool { return target == os.ErrNotExist }

func originalSourceError(err error) error {
	original := err
	// Openat may return a bare errno; only call after a source descriptor
	// was authorized. Reject joined trees and all non-ENOENT leaves.
	// Never classify a joined tree here, even if one branch reports ENOENT.
	for err != nil {
		if err == syscall.ENOENT { //nolint:errorlint // Exact leaf only; do not let Is hide a joined or non-source error.
			return &sourceAbsence{}
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = wrapped.Unwrap()
	}
	return original
}

// Resolve and Lstat must report an actual source path/link failure; a bare
// errno from unrelated resolution work (such as getwd) is not absence.
func originalResolvedSourceError(err error) error {
	var path *os.PathError
	var link *os.LinkError
	if errors.As(err, &path) || errors.As(err, &link) {
		return originalSourceError(err)
	}
	return err
}

func onlySourceAbsence(err error) bool {
	return err != nil && leavesAre(err, func(leaf error) bool {
		_, ok := errors.AsType[*sourceAbsence](leaf)
		return ok
	})
}

// Preserve a fatal policy-store classification without exposing the callback's
// private diagnostic or making a mixed cancellation look like clean shutdown.
type policyStorageFailure struct{}

func (*policyStorageFailure) Error() string { return "sessioncapture: policy storage unavailable" }
