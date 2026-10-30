//go:build !darwin && !linux

package capturesource

import "os"

// No portable ctime/inode: never claim a source is unchanged.
func statFingerprint(os.FileInfo) string { return "" }
