package store

import "testing"

// SetMultipart shrinks the multipart threshold and part size for one test.
func SetMultipart(t *testing.T, threshold int64, part int) {
	t.Helper()
	oldThreshold, oldPart := maxPutSize, partSize
	maxPutSize, partSize = threshold, part
	t.Cleanup(func() { maxPutSize, partSize = oldThreshold, oldPart })
}
