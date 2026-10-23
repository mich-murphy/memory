//go:build darwin || linux

package sessioncapture

import (
	"errors"

	"golang.org/x/sys/unix"
)

func availableBytes(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return filesystemAvailable(st.Bavail, int64(st.Bsize))
}

func filesystemAvailable(blocks uint64, blockSize int64) (int64, error) {
	if blockSize <= 0 {
		return 0, errors.New("sessioncapture: invalid filesystem block size")
	}
	// Saturate rather than overflow on unusually large filesystems.
	const maxInt64 = int64(1<<63 - 1)
	if blocks > uint64(maxInt64)/uint64(blockSize) {
		return maxInt64, nil
	}
	return int64(blocks) * blockSize, nil
}
