package sessioncapture

import (
	"errors"
	"testing"
	"time"
)

func TestValidateCadence(t *testing.T) {
	s := time.Second
	for _, tc := range []struct {
		name string
		q, m time.Duration
		ok   bool
	}{
		{"zero", 0, 0, true},
		{"normal", 30 * s, 600 * s, true},
		{"equal", 5 * s, 5 * s, true},
		{"negative quiet", -s, 0, false},
		{"negative max", 0, -s, false},
		{"max below quiet", 30 * s, 10 * s, false},
		{"quiet without max", s, 0, false},
		{"quiet too large", MaxQuietPeriod + s, MaxCaptureDelay, false},
		{"max too large", s, MaxCaptureDelay + s, false},
		{"bounds", MaxQuietPeriod, MaxCaptureDelay, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCadence(RuntimeOptions{QuietPeriod: tc.q, MaxDelay: tc.m})
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, ErrCadence)) {
				t.Fatalf("validateCadence(%s,%s)=%v, want ok=%v", tc.q, tc.m, err, tc.ok)
			}
		})
	}
}

// The quiet period restarts on every new observation but the maximum delay is
// measured from the first unconsumed one. The clock is a variable, never a sleep.
func TestSourceDebounceQuietAndMaxDelay(t *testing.T) {
	dir, o, r := setup(t)
	now := time.Unix(1000, 0)
	o.Now = func() time.Time { return now }
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	q.runtimeLocal().enrichmentOptions.Store(&RuntimeOptions{QuietPeriod: 5 * time.Second, MaxDelay: 12 * time.Second})
	claimed := func() bool {
		t.Helper()
		w, err := q.claimSource(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if w.id != 0 {
			q.releaseSourceClaim(t.Context(), w)
		}
		return w.id != 0
	}
	hintAt := func(sec int) {
		t.Helper()
		now = time.Unix(1000, 0).Add(time.Duration(sec) * time.Second)
		if err := q.Hint(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	at := func(sec int) {
		now = time.Unix(1000, 0).Add(time.Duration(sec) * time.Second)
	}
	hintAt(0)
	if claimed() {
		t.Fatal("claimed inside the quiet period")
	}
	st, err := q.RuntimeStatus(t.Context())
	if err != nil || st.DebouncedSources != 1 {
		t.Fatalf("status %+v %v, want 1 debounced", st, err)
	}
	hintAt(4)
	hintAt(8)
	hintAt(11) // quiet would end at 16, the max delay caps it at 12
	at(11)
	st, err = q.RuntimeStatus(t.Context())
	if err != nil || st.DebounceLagNS != int64(11*time.Second) {
		t.Fatalf("lag %d %v, want 11s", st.DebounceLagNS, err)
	}
	at(12)
	if !claimed() {
		t.Fatal("max delay did not release the source")
	}
	st, err = q.RuntimeStatus(t.Context())
	if err != nil || st.DebouncedSources != 0 || st.DebounceLagNS != 0 {
		t.Fatalf("status after release %+v %v", st, err)
	}
}

func TestZeroQuietPeriodCapturesImmediately(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	w, err := q.claimSource(t.Context())
	if err != nil || w.id == 0 {
		t.Fatalf("claim %+v %v, want immediate", w, err)
	}
}
