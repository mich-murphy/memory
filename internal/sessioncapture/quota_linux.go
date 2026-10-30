//go:build linux

package sessioncapture

import "golang.org/x/sys/unix"

func filesystemBlockSize(st *unix.Statfs_t) int64 {
	return st.Bsize
}
