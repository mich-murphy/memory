package publicationr2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// BodyBudget bounds object-body transfers (PUT and verification reads). Metadata,
// reference and conditional-write operations keep the flat Backend timeout; only
// object bodies scale with size. The defaults are conservative and not measured
// against live R2: MinThroughput in particular is a placeholder to be confirmed.
type BodyBudget struct {
	// Base is a fixed allowance added to size/MinThroughput.
	Base time.Duration
	// MinThroughput is the slowest acceptable sustained rate in bytes per second.
	MinThroughput int64
	// Floor and Cap clamp the total budget of one object transfer.
	Floor, Cap time.Duration
	// Idle is the longest a transfer may make no body progress before it is
	// abandoned as stalled.
	Idle time.Duration
}

// DefaultMinThroughput is 1 MiB/s: conservative, not measured.
const DefaultMinThroughput int64 = 1 << 20

// DefaultBodyBudget returns the documented defaults: 30s + size/(1 MiB/s),
// clamped to [60s, 15min], with a 30s no-progress limit.
func DefaultBodyBudget() BodyBudget {
	return BodyBudget{Base: 30 * time.Second, MinThroughput: DefaultMinThroughput, Floor: time.Minute, Cap: 15 * time.Minute, Idle: 30 * time.Second}
}

func (b BodyBudget) validate() error {
	if b.Base < 0 || b.MinThroughput <= 0 || b.Floor <= 0 || b.Cap < b.Floor || b.Idle <= 0 {
		return errors.New("invalid publication body budget")
	}
	return nil
}

// Total is the whole-transfer deadline for an object of size bytes.
func (b BodyBudget) Total(size int64) time.Duration {
	size = min(max(size, 0), MaxObjectSize)
	d := b.Base + time.Duration(size)*time.Second/time.Duration(b.MinThroughput)
	return min(max(d, b.Floor), b.Cap)
}

// TransferBudget is the worst case of one object PutFile: the upload and, after a
// lost acknowledgement, the read-back verification each get a full body budget.
func (b *Backend) TransferBudget(size int64) time.Duration { return 2 * b.body.Total(size) }

var _ publication.TransferBudgeter = (*Backend)(nil)

// WithBodyBudget replaces the default object-body budget.
func WithBodyBudget(budget BodyBudget) Option {
	return func(b *Backend) error {
		if err := budget.validate(); err != nil {
			return err
		}
		b.body = budget
		return nil
	}
}

// Option configures a Backend.
type Option func(*Backend) error

// errStalled is a retryable timeout: it wraps context.DeadlineExceeded so the
// runtime classifies it as a timeout, never as a deterministic failure.
var errStalled = fmt.Errorf("publication transfer stalled: %w", context.DeadlineExceeded)

// ctxErr reports why ctx ended, preserving timeout causes set by a transfer.
func ctxErr(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		return nil
	}
	if cause := context.Cause(ctx); errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return err
}

// transfer bounds one request with a resettable total deadline and an optional
// idle watchdog. Cancelling with a cause lets callers tell timeouts from shutdown.
type transfer struct {
	cancel context.CancelCauseFunc
	total  *time.Timer
	idle   *idleGuard
}

func (b *Backend) newTransfer(parent context.Context) (context.Context, *transfer) {
	ctx, cancel := context.WithCancelCause(parent)
	return ctx, &transfer{cancel: cancel, total: time.AfterFunc(b.timeout, func() { cancel(context.DeadlineExceeded) })}
}

// budget replaces the remaining total deadline.
func (t *transfer) budget(d time.Duration) { t.total.Reset(d) }

// guard installs the idle watchdog, replacing any previous one.
func (t *transfer) guard(idle time.Duration, armed bool) *idleGuard {
	t.idle.stop()
	g := &idleGuard{idle: idle}
	g.timer = time.AfterFunc(idle, func() { t.cancel(errStalled) })
	if !armed {
		g.timer.Stop()
	}
	t.idle = g
	return g
}

func (t *transfer) stop() {
	t.total.Stop()
	t.idle.stop()
	t.cancel(nil)
}

type idleGuard struct {
	timer *time.Timer
	idle  time.Duration
}

func (g *idleGuard) arm() {
	if g != nil {
		g.timer.Reset(g.idle)
	}
}

func (g *idleGuard) disarm() {
	if g != nil {
		g.timer.Stop()
	}
}

func (g *idleGuard) stop() { g.disarm() }

// progressReader restarts the idle timer after each chunk and disarms it at EOF,
// when only the destination's response remains (bounded by the total deadline).
type progressReader struct {
	r io.Reader
	g *idleGuard
}

func (p progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if err != nil {
		p.g.disarm()
	} else {
		p.g.arm()
	}
	return n, err
}

// progressSeeker keeps the body seekable for the SDK. Seeking does not arm the
// guard: only reads do, so an idle wait for the response after a rewind is not a stall.
type progressSeeker struct {
	progressReader
	s io.Seeker
}

func (p progressSeeker) Seek(offset int64, whence int) (int64, error) {
	return p.s.Seek(offset, whence)
}
