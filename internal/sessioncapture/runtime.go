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
	// ErrReplayResolved is returned after Replay deactivated a quarantine whose
	// work had already moved on; the work itself was not revived.
	ErrReplayResolved = errors.New("sessioncapture: quarantine resolved without revival")
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
	// FailureConflict is a guarded-write or immutable-object conflict. It is a
	// distinct, visible class: it is neither retried unseen forever nor treated as
	// a network blip, so it spends the quarantine budget and stays replayable.
	// A rebuilt spool behind the remote converges by rebasing (rebase.go); what
	// remains here is divergence that cannot be reconciled.
	FailureConflict FailureClass = "conflict"
	// FailureRejected is a destination rejection (HTTP 4xx) that repeating the same
	// request cannot fix.
	FailureRejected FailureClass = "rejected"
	// FailureSpoolFull is capacity backpressure that clears when space returns.
	FailureSpoolFull FailureClass = "spool-full"
	// FailureSourceLimit marks a session at publication.MaxSources. Retrying cannot
	// help, so the source parks in the visible terminal state "source-limit"
	// without a quarantine entry or further attempts.
	FailureSourceLimit FailureClass = "source-limit"
)

// spendsQuarantineBudget reports whether repeated failures of this class count
// toward MaxAttempts. Only deterministic classes do. Transient, timeout and
// throttling failures retry indefinitely with capped, jittered backoff
// (contract section 3); destination failures pause instead of quarantining.
func (c FailureClass) spendsQuarantineBudget() bool {
	switch c {
	case FailurePolicy, FailureIntegrity, FailureQuota, FailureConflict, FailureRejected:
		return true
	case FailureTransient, FailureDestination, FailureTimeout, FailureAbsent, FailureSpoolFull, FailureSourceLimit:
		return false
	}
	return false
}

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
	// QuietPeriod (Q) delays capturing a source until it has been unchanged this long;
	// MaxDelay (M) caps the delay from the first unconsumed observation. Zero Q
	// captures immediately. See validateCadence for bounds.
	QuietPeriod, MaxDelay time.Duration
	// Jitter returns the desired delay; its result is clamped to [1s,5min].
	Jitter   func(time.Duration) time.Duration
	Classify func(error) FailureClass

	// budgeted is set by Run when the backend reports per-object transfer budgets.
	budgeted bool
}

type runtimeLocal struct {
	mu                   sync.Mutex
	initialized, running bool
	sourceCursor         int64
	sessionCursors       [2]int64 // Raw and optional delivery scan independently.
	nativeCursor         int
	discoveryWake        chan struct{}
	wakeSeen             map[string]string
	wakeSnapshot         map[string]discoveryHint
	enrichmentOptions    atomic.Pointer[RuntimeOptions]
	softwareVersion      atomic.Value // string; retained only for new quarantine entries
}

func (q *Queue) runtimeLocal() *runtimeLocal {
	q.runtimeOnce.Do(func() { q.runtime = &runtimeLocal{discoveryWake: make(chan struct{}, 1)} })
	return q.runtime
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
	case errors.Is(err, ErrSourceLimit):
		return FailureSourceLimit
	case errors.Is(err, ErrSpoolFull):
		return FailureSpoolFull
	case errors.Is(err, ErrQuota):
		return FailureQuota
	case errors.Is(err, publication.ErrConflict):
		return FailureConflict
	case errors.Is(err, publication.ErrRejected):
		return FailureRejected
	case errors.Is(err, context.DeadlineExceeded):
		return FailureTimeout
	}
	if custom != nil {
		switch c := custom(err); c {
		case FailureTransient, FailureDestination, FailurePolicy, FailureIntegrity, FailureQuota, FailureTimeout, FailureAbsent, FailureConflict, FailureRejected, FailureSpoolFull, FailureSourceLimit:
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
	if err := validateCadence(o); err != nil {
		return err
	}
	_, o.budgeted = b.(publication.TransferBudgeter)
	r.softwareVersion.Store(o.SoftwareVersion)
	r.enrichmentOptions.Store(&o)
	// A restart flushes debounced sources: their observations are durable.
	if _, err := q.db.ExecContext(ctx, `UPDATE runtime_sources SET debounce_until=0 WHERE debounce_until>0`); err != nil {
		return runtimeStopError(ctx, err)
	}
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
			return q.runtimeWorkerNamed(ctx, "capture", o, func(c context.Context) (bool, error) { return q.captureNext(c, o) })
		})
	}
	for range o.DeliveryWorkers {
		launch(func(ctx context.Context) error {
			return q.runtimeWorkerNamed(ctx, "delivery", o, func(c context.Context) (bool, error) { return q.deliveryNext(c, b, o) })
		})
	}
	// Exactly one additional worker owns optional publication network work.
	// Raw DeliveryWorkers never spend their budgets on selected optional objects.
	launch(func(ctx context.Context) error {
		return q.runtimeWorkerNamed(ctx, "optional-delivery", o, func(c context.Context) (bool, error) { return q.deliveryLaneNext(c, b, o, true) })
	})
	launch(q.inboxLoop)
	launch(func(ctx context.Context) error {
		return q.runtimeWorkerNamed(ctx, "optional-repair", o, func(c context.Context) (bool, error) { return q.optionalRepairNext(c, b, o) })
	})
	if q.opts.EnableEnrichment {
		launch(q.nativeInboxLoop)
		launch(func(ctx context.Context) error {
			return q.runtimeWorkerNamed(ctx, "enrichment", o, func(c context.Context) (bool, error) { return q.enrichmentNext(c, o) })
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
	return q.runtimeWorkerNamed(ctx, "worker", o, work)
}

// runtimeWorkerNamed skips a step that failed only with an operation deadline or
// SQLITE_BUSY (logged, counted, retried next turn); every other error still stops Run.
func (q *Queue) runtimeWorkerNamed(ctx context.Context, name string, o RuntimeOptions, work func(context.Context) (bool, error)) error {
	timer := time.NewTicker(min(o.PollInterval, 100*time.Millisecond))
	defer timer.Stop()
	for ctx.Err() == nil {
		op, cancel := stepContext(ctx, name, o)
		did, err := work(op)
		opExpired := errors.Is(op.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			if !transientStepError(ctx, opExpired, err) {
				return runtimeStopError(ctx, err)
			}
			q.noteStepError(ctx, name, err)
			did = false
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

// stepContext bounds one worker turn by OperationTimeout. Workers that move object
// bodies are not bounded here: their attempt context is OperationTimeout plus the
// backend's per-object allowance for the manifest being moved (transferAllowance),
// which is only known once the head job is loaded.
func stepContext(ctx context.Context, name string, o RuntimeOptions) (context.Context, context.CancelFunc) {
	switch name {
	case "delivery", "optional-delivery", "optional-repair":
		if o.budgeted {
			return context.WithCancel(ctx)
		}
	}
	return context.WithTimeout(ctx, o.OperationTimeout)
}

// transferAllowance sums the backend's worst-case PutFile duration over the
// selected objects of the given sources; zero when the backend reports none.
func transferAllowance(b publication.Backend, sources ...publication.Source) time.Duration {
	tb, ok := b.(publication.TransferBudgeter)
	if !ok {
		return 0
	}
	var d time.Duration
	for i := range sources {
		if publication.IsSelected(sources[i]) {
			d += tb.TransferBudget(sources[i].Object.EncodedSize)
		}
	}
	return d
}

func (q *Queue) discoverLoop(ctx context.Context, o RuntimeOptions) error {
	timer := time.NewTicker(o.PollInterval)
	defer timer.Stop()
	for {
		if err := q.discoverOnce(ctx, o); err != nil {
			if !transientStepError(ctx, false, err) {
				return err
			}
			q.noteStepError(ctx, "discovery", err)
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

// shutdownCancellation is true only when the supplied (operation) context ended
// by cancellation of its parent, not by its own deadline, and the failure is
// nothing but that cancellation. Any other leaf (durability, SQLite, a real
// timeout) keeps the failure genuine.
func shutdownCancellation(ctx context.Context, err error) bool {
	return ctx.Err() == context.Canceled && pureCancellation(err, context.Canceled)
}

func pureCancellation(err, canceled error) bool {
	return err != nil && leavesAre(err, func(leaf error) bool {
		return leaf == canceled //nolint:errorlint // Leaves must equal the caller sentinel; custom Is must not hide other failures.
	})
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
