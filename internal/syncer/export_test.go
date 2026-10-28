package syncer

import (
	"testing"
	"time"
)

func SetLimits(t *testing.T, largeFile, chunk int64, recheck time.Duration) {
	t.Helper()
	oldLarge, oldChunk, oldRecheck := largeFileSize, chunkSize, snapshotRecheck
	largeFileSize, chunkSize, snapshotRecheck = largeFile, chunk, recheck
	t.Cleanup(func() { largeFileSize, chunkSize, snapshotRecheck = oldLarge, oldChunk, oldRecheck })
}

// SetMaxSegments lowers the fold boundary for one test. Callers must not be parallel.
func SetMaxSegments(t *testing.T, n int) {
	t.Helper()
	old := maxSegments
	maxSegments = n
	t.Cleanup(func() { maxSegments = old })
}
