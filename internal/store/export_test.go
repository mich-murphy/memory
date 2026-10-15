package store

import (
	"testing"
	"time"
)

// SetOpTimeout shrinks the per-request R2 deadline for one test.
func SetOpTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := opTimeout
	opTimeout = d
	t.Cleanup(func() { opTimeout = old })
}

// SetMultipart shrinks the multipart threshold and part size for one test.
func SetMultipart(t *testing.T, threshold int64, part int) {
	t.Helper()
	oldThreshold, oldPart := maxPutSize, partSize
	maxPutSize, partSize = threshold, part
	t.Cleanup(func() { maxPutSize, partSize = oldThreshold, oldPart })
}
