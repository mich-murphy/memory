//go:build darwin

package capturesource

import "syscall"

func statTimes(st *syscall.Stat_t) (mtime, ctime int64) {
	return st.Mtimespec.Nano(), st.Ctimespec.Nano()
}

func statDeviceIdentity(st *syscall.Stat_t) uint64 {
	// Preserve stat-v1: a negative dev_t wraps directly to uint64, not uint32.
	return uint64(st.Dev)
}
