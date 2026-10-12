package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// ErrLocked means another run held the sync lock for the whole timeout.
var ErrLocked = errors.New("another agent-sessions run is holding the lock")

// Lock serialises hook-triggered and sweep runs so the same file isn't uploaded twice at once.
// It returns the function that releases the lock.
func Lock(ctx context.Context, dir string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	fl := flock.New(filepath.Join(dir, "sync.lock"))
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ok, err := fl.TryLockContext(ctx, 250*time.Millisecond)
	if !ok {
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("lock: %w", err)
		}
		return nil, ErrLocked
	}
	return func() { _ = fl.Unlock() }, nil
}
