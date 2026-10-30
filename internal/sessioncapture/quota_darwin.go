//go:build darwin

package sessioncapture

import "golang.org/x/sys/unix"

func filesystemBlockSize(st *unix.Statfs_t) int64 {
	return int64(st.Bsize)
}
