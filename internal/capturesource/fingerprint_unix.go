//go:build darwin || linux

package capturesource

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"syscall"
)

// statFingerprint digests the stat identity of a file already observed by
// discovery: device, inode, size, mtime and ctime (ns). ctime cannot be restored
// by utimes, so a same-size rewrite with a restored mtime still changes it.
// Empty means unknown, which makes the queue treat every sweep as a change.
func statFingerprint(info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return ""
	}
	mt, ct := statTimes(st)
	sum := sha256.Sum256([]byte(fmt.Sprintf("stat-v1:%d:%d:%d:%d:%d", statDeviceIdentity(st), st.Ino, st.Size, mt, ct)))
	return hex.EncodeToString(sum[:])
}
