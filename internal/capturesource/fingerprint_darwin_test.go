//go:build darwin

package capturesource

import (
	"math"
	"syscall"
	"testing"
)

func TestStatDeviceIdentity(t *testing.T) {
	for _, test := range []struct {
		device int32
		want   uint64
	}{
		{0, 0},
		{1, 1},
		{math.MaxInt32, math.MaxInt32},
		{-1, math.MaxUint64},
		{math.MinInt32, 18446744071562067968},
	} {
		if got := statDeviceIdentity(&syscall.Stat_t{Dev: test.device}); got != test.want {
			t.Fatalf("device %d normalized to %d, want %d", test.device, got, test.want)
		}
	}
}

func TestStatFingerprintV1(t *testing.T) {
	st := &syscall.Stat_t{
		Dev:       -1,
		Ino:       math.MaxUint64,
		Size:      -17,
		Mtimespec: syscall.Timespec{Sec: 123, Nsec: 456},
		Ctimespec: syscall.Timespec{Sec: -2, Nsec: 789},
	}
	assertStatFingerprintV1(t, st, "stat-v1:18446744073709551615:18446744073709551615:-17:123000000456:-1999999211")
}
