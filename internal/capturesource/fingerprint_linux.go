//go:build linux

package capturesource

import "syscall"

func statTimes(st *syscall.Stat_t) (mtime, ctime int64) {
	return st.Mtim.Nano(), st.Ctim.Nano()
}

func statDeviceIdentity(st *syscall.Stat_t) uint64 {
	return st.Dev
}
