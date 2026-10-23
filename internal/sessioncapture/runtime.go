package sessioncapture

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// ErrDestination identifies authentication/configuration failures. Transport
// adapters may wrap it, or supply RuntimeOptions.Classify for SDK-specific errors.
var (
	ErrDestination    = errors.New("sessioncapture: destination unavailable")
	ErrQuarantined    = errors.New("sessioncapture: work quarantined")
	ErrRuntimeRunning = errors.New("sessioncapture: coordinator already running")
)

type FailureClass string

const (
	FailureTransient   FailureClass = "transient"
	FailureDestination FailureClass = "destination"
	FailurePolicy      FailureClass = "policy-blocked"
	FailureIntegrity   FailureClass = "integrity-error"
	FailureQuota       FailureClass = "quota"
	FailureTimeout     FailureClass = "timeout"
	FailureAbsent      FailureClass = "absent"
)

type RuntimeOptions struct {
	Enrich               EnrichFunc
	CheckEnrichmentAsset CheckEnrichmentAssetFunc
	// SoftwareVersion is diagnostic provenance, not a build attestation.
	SoftwareVersion                          string
	Discover                                 func(context.Context) ([]Request, error)
	DiscoverBatch                            func(context.Context) (DiscoveryBatch, error)
	PollInterval                             time.Duration
	Workers, CaptureWorkers, DeliveryWorkers int
	OperationTimeout                         time.Duration
	MaxAttempts                              int
	// Jitter returns the desired delay; its result is clamped to [1s,5min].
	Jitter   func(time.Duration) time.Duration
	Classify func(error) FailureClass
}

type runtimeLocal struct {
	mu                          sync.Mutex
	initialized, running        bool
	sourceCursor, sessionCursor int64
	nativeCursor                int
	discoveryWake               chan struct{}
	wakeSeen                    map[string]string
	wakeSnapshot                map[string]discoveryHint
	enrichmentOptions           atomic.Pointer[RuntimeOptions]
	softwareVersion             atomic.Value // string; retained only for new quarantine entries
}

func (q *Queue) runtimeLocal() *runtimeLocal {
	v, _ := q.sessions.LoadOrStore("\x00sessioncapture-runtime", &runtimeLocal{discoveryWake: make(chan struct{}, 1)})
	r, ok := v.(*runtimeLocal)
	if !ok {
		panic("sessioncapture: invalid runtime state")
	}
	return r
}

func runtimeDefaults(o RuntimeOptions) RuntimeOptions {
	if o.SoftwareVersion == "" {
		o.SoftwareVersion = runtimeBuildVersion()
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 30 * time.Second
	}
	if o.OperationTimeout <= 0 {
		o.OperationTimeout = 60 * time.Second
	}
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.CaptureWorkers <= 0 {
		o.CaptureWorkers = o.Workers
	}
	if o.DeliveryWorkers <= 0 {
		o.DeliveryWorkers = o.Workers
	}
	o.CaptureWorkers = min(o.CaptureWorkers, 64)
	o.DeliveryWorkers = min(o.DeliveryWorkers, 64)
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 8
	}
	return o
}

func classify(err error, custom func(error) FailureClass) FailureClass {
	switch {
	case errors.Is(err, ErrDestination):
		return FailureDestination
	case errors.Is(err, ErrPolicy):
		return FailurePolicy
	case errors.Is(err, ErrIntegrity):
		return FailureIntegrity
	case errors.Is(err, ErrQuota):
		return FailureQuota
	case errors.Is(err, context.DeadlineExceeded):
		return FailureTimeout
	}
	if custom != nil {
		switch c := custom(err); c {
		case FailureTransient, FailureDestination, FailurePolicy, FailureIntegrity, FailureQuota, FailureTimeout, FailureAbsent:
			return c
		}
	}
	return FailureTransient
}

func retryDelay(attempts int, jitter func(time.Duration) time.Duration) time.Duration {
	d := min(time.Second*time.Duration(1<<min(max(attempts-1, 0), 9)), 5*time.Minute)
	if jitter != nil {
		d = jitter(d)
	} else {
		var entropy [8]byte
		if _, err := rand.Read(entropy[:]); err == nil {
			d = d/2 + time.Duration(binary.LittleEndian.Uint64(entropy[:])%uint64(d/2+1))
		}
	}
	return min(max(d, time.Second), 5*time.Minute)
}

// Run blocks until cancellation or a local work-store error. It starts no daemon.
// All operations, including discovery, have independent context budgets. Backends
// and discovery callbacks must cooperate with cancellation; Run joins every worker.
func (q *Queue) Run(ctx context.Context, b publication.Backend, options RuntimeOptions) error {
	if options.Discover != nil && options.DiscoverBatch != nil {
		return errors.New("sessioncapture: choose one discovery callback")
	}
	if err := q.InitRuntime(ctx); err != nil {
		return runtimeStopError(ctx, err)
	}
	r := q.runtimeLocal()
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrRuntimeRunning
	}
	r.running = true
	r.wakeSnapshot = nil
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()
	o := runtimeDefaults(options)
	r.softwareVersion.Store(o.SoftwareVersion)
	r.enrichmentOptions.Store(&o)
	if err := q.startDiscoverySweep(ctx); err != nil {
		return runtimeStopError(ctx, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	launch := func(f func(context.Context) error) {
		wg.Go(func() {
			if err := runtimeStopError(ctx, f(ctx)); err != nil {
				select {
				case errs <- err:
				default:
				}
				cancel()
			}
		})
	}
	for range o.CaptureWorkers {
		launch(func(ctx context.Context) error {
			return q.runtimeWorker(ctx, o, func(c context.Context) (bool, error) { return q.captureNext(c, o) })
		})
	}
	for range o.DeliveryWorkers {
		launch(func(ctx context.Context) error {
			return q.runtimeWorker(ctx, o, func(c context.Context) (bool, error) { return q.deliveryNext(c, b, o) })
		})
	}
	// Exactly one additional worker owns optional publication network work.
	// Raw DeliveryWorkers never spend their budgets on selected optional objects.
	launch(func(ctx context.Context) error {
		return q.runtimeWorker(ctx, o, func(c context.Context) (bool, error) { return q.deliveryLaneNext(c, b, o, true) })
	})
	launch(q.inboxLoop)
	launch(func(ctx context.Context) error {
		return q.runtimeWorker(ctx, o, func(c context.Context) (bool, error) { return q.optionalRepairNext(c, b, o) })
	})
	if q.opts.EnableEnrichment {
		launch(q.nativeInboxLoop)
		launch(func(ctx context.Context) error {
			return q.runtimeWorker(ctx, o, func(c context.Context) (bool, error) { return q.enrichmentNext(c, o) })
		})
	}
	if o.Discover != nil || o.DiscoverBatch != nil {
		launch(func(ctx context.Context) error { return q.discoverLoop(ctx, o) })
	}
	<-ctx.Done()
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

func (q *Queue) runtimeWorker(ctx context.Context, o RuntimeOptions, work func(context.Context) (bool, error)) error {
	timer := time.NewTicker(min(o.PollInterval, 100*time.Millisecond))
	defer timer.Stop()
	for ctx.Err() == nil {
		op, cancel := context.WithTimeout(ctx, o.OperationTimeout)
		did, err := work(op)
		cancel()
		if err != nil {
			return runtimeStopError(ctx, err)
		}
		if !did {
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
			}
		}
	}
	return nil
}

func (q *Queue) discoverLoop(ctx context.Context, o RuntimeOptions) error {
	timer := time.NewTicker(o.PollInterval)
	defer timer.Stop()
	for {
		if err := q.discoverOnce(ctx, o); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		case <-q.runtimeLocal().discoveryWake:
		}
	}
}

// runtimeStopError suppresses only a pure caller shutdown. errors.Is alone is
// insufficient: Join(context.Canceled, fsyncError) must still fail the run.
func runtimeStopError(ctx context.Context, err error) error {
	if ctx.Err() != nil && pureCancellation(err, ctx.Err()) {
		return nil
	}
	return err
}

func pureCancellation(err, canceled error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !pureCancellation(child, canceled) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return pureCancellation(wrapped.Unwrap(), canceled)
	}
	return err == canceled //nolint:errorlint // Leaves must equal the caller sentinel; custom Is must not hide other failures.
}

// runtimeBuildVersion reports only metadata actually embedded by the Go builder.
// Development builds without revision metadata do not acquire a fake identity.
func runtimeBuildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown/devel"
	}
	parts := []string{}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		parts = append(parts, info.Main.Version)
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" {
			parts = append(parts, setting.Key+"="+setting.Value)
		}
	}
	if len(parts) == 0 {
		return "unknown/devel"
	}
	return strings.Join(parts, " ")
}

func (q *Queue) runtimeSoftwareVersion() string {
	if version := q.runtimeLocal().softwareVersion.Load(); version != nil {
		if text, ok := version.(string); ok {
			return text
		}
	}
	return runtimeBuildVersion()
}
