//go:build darwin

package sessioncapture

import (
	"math"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFilesystemBlockSize(t *testing.T) {
	for _, size := range []uint32{0, 4096, math.MaxUint32} {
		st := &unix.Statfs_t{Bsize: size}
		got := filesystemBlockSize(st)
		if got != int64(size) {
			t.Fatalf("block size %d normalized to %d", size, got)
		}
	}
}
