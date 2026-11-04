package sessioncapture

import (
	"errors"
	"fmt"
	"time"
)

// Bounds for the capture debounce. Q is the per-source quiescence period, M the
// maximum delay from the first unconsumed observation.
const (
	MaxQuietPeriod  = time.Hour
	MaxCaptureDelay = 24 * time.Hour
)

// Daemon defaults chosen by reports/g3-cadence.md (replay sweep over D's timelines):
// Q=30s cuts p99 captures per session by 72% and PUTs by 82% against immediate
// capture; M=300s bounds worst-case freshness lag to about five minutes.
// RuntimeOptions zero values still mean immediate capture.
const (
	DefaultQuietPeriod = 30 * time.Second
	DefaultMaxDelay    = 300 * time.Second
)

// ErrCadence reports an invalid QuietPeriod/MaxDelay combination.
var ErrCadence = errors.New("sessioncapture: invalid capture cadence")

// validateCadence rejects out-of-range values. A positive Q needs a positive M no
// smaller than Q, so continuous writes cannot starve capture.
func validateCadence(o RuntimeOptions) error {
	switch {
	case o.QuietPeriod < 0 || o.MaxDelay < 0:
		return fmt.Errorf("%w: negative duration", ErrCadence)
	case o.QuietPeriod > MaxQuietPeriod || o.MaxDelay > MaxCaptureDelay:
		return fmt.Errorf("%w: quiet period above %s or max delay above %s", ErrCadence, MaxQuietPeriod, MaxCaptureDelay)
	case o.QuietPeriod > 0 && o.MaxDelay < o.QuietPeriod:
		return fmt.Errorf("%w: max delay %s below quiet period %s", ErrCadence, o.MaxDelay, o.QuietPeriod)
	}
	return nil
}

// cadence returns the active debounce; it is zero (capture immediately) before Run
// stores the validated options.
func (q *Queue) cadence() (quiet, maxDelay time.Duration) {
	if o := q.runtimeLocal().enrichmentOptions.Load(); o != nil {
		return o.QuietPeriod, o.MaxDelay
	}
	return 0, 0
}
