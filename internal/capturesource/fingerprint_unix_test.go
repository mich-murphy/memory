//go:build darwin || linux

package capturesource

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"syscall"
	"testing"
)

type fingerprintFileInfo struct {
	os.FileInfo
	stat any
}

func (info fingerprintFileInfo) Sys() any { return info.stat }

func assertStatFingerprintV1(t *testing.T, st *syscall.Stat_t, preimage string) {
	t.Helper()
	sum := sha256.Sum256([]byte(preimage))
	want := hex.EncodeToString(sum[:])
	if got := statFingerprint(fingerprintFileInfo{stat: st}); got != want {
		t.Fatalf("stat-v1 fingerprint = %q, want %q (preimage %q)", got, want, preimage)
	}
}

func TestStatFingerprintUnknown(t *testing.T) {
	for _, stat := range []any{nil, (*syscall.Stat_t)(nil), "not a stat"} {
		if got := statFingerprint(fingerprintFileInfo{stat: stat}); got != "" {
			t.Fatalf("unknown stat fingerprint = %q", got)
		}
	}
}
