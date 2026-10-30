//go:build linux

package sessioncapture

import (
	"math"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFilesystemBlockSize(t *testing.T) {
	for _, size := range []int64{0, -1, math.MinInt64, 4096, math.MaxInt64} {
		st := &unix.Statfs_t{Bsize: size}
		got := filesystemBlockSize(st)
		if got != size {
			t.Fatalf("block size %d normalized to %d", size, got)
		}
	}
}
