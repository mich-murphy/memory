//go:build linux

package capturesource

import (
	"math"
	"syscall"
	"testing"
)

func TestStatDeviceIdentity(t *testing.T) {
	for _, test := range []struct {
		device uint64
		want   uint64
	}{
		{0, 0},
		{1, 1},
		{math.MaxUint64, math.MaxUint64},
	} {
		if got := statDeviceIdentity(&syscall.Stat_t{Dev: test.device}); got != test.want {
			t.Fatalf("device %d normalized to %d, want %d", test.device, got, test.want)
		}
	}
}

func TestStatFingerprintV1(t *testing.T) {
	st := &syscall.Stat_t{
		Dev:  math.MaxUint64,
		Ino:  math.MaxUint64,
		Size: -17,
		Mtim: syscall.Timespec{Sec: 123, Nsec: 456},
		Ctim: syscall.Timespec{Sec: -2, Nsec: 789},
	}
	assertStatFingerprintV1(t, st, "stat-v1:18446744073709551615:18446744073709551615:-17:123000000456:-1999999211")
}
