package syncer

import (
	"testing"
	"time"
)

// SetLimits shrinks the large-file threshold, read chunk and snapshot recheck interval for one
// test.
func SetLimits(t *testing.T, largeFile, chunk int64, recheck time.Duration) {
	t.Helper()
	oldLarge, oldChunk, oldRecheck := largeFileBytes, chunkSize, snapshotRecheck
	largeFileBytes, chunkSize, snapshotRecheck = largeFile, chunk, recheck
	t.Cleanup(func() { largeFileBytes, chunkSize, snapshotRecheck = oldLarge, oldChunk, oldRecheck })
}

// FoldAfter is how long a file must be quiet before its segments are folded.
const FoldAfter = foldAfter
