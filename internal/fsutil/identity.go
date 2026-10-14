package fsutil

import "os"

// SameLocation accepts matching path spellings (including not-yet-created paths), or
// existing filesystem aliases. It does not conflate distinct case-sensitive locations.
// An unavailable path cannot establish an additional filesystem identity match.
func SameLocation(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	first, err := os.Stat(left)
	if err != nil {
		return false
	}
	second, err := os.Stat(right)
	return err == nil && os.SameFile(first, second)
}
