package syncer

import (
	"testing"
	"time"
)

func SetLimits(t *testing.T, largeFile, chunk int64, recheck time.Duration) {
	t.Helper()
	oldLarge, oldChunk, oldRecheck := largeFileBytes, chunkSize, snapshotRecheck
	largeFileBytes, chunkSize, snapshotRecheck = largeFile, chunk, recheck
	t.Cleanup(func() { largeFileBytes, chunkSize, snapshotRecheck = oldLarge, oldChunk, oldRecheck })
}

const FoldAfter = foldAfter
