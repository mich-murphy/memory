package sessioncapture

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

type fatalBoundaryBackend struct {
	publication.Backend
	key, stage       string
	cause            error
	cancel           context.CancelFunc
	entered, release chan struct{}
	once             sync.Once
	attempts         chan struct{}
}

func (b *fatalBoundaryBackend) fail(ctx context.Context, stage, key string) error {
	if stage != b.stage || key != b.key {
		return nil
	}
	if b.entered != nil {
		b.once.Do(func() {
			close(b.entered)
			select {
			case <-b.release:
			case <-ctx.Done():
			}
		})
	}
	if b.attempts != nil {
		select {
		case b.attempts <- struct{}{}:
		default:
		}
	}
	if b.cancel != nil {
		b.cancel()
	}
	return b.cause
}

func (b *fatalBoundaryBackend) PutFile(ctx context.Context, key, path string) error {
	if err := b.fail(ctx, "upload", key); err != nil {
		return err
	}
	return b.Backend.PutFile(ctx, key, path)
}

func (b *fatalBoundaryBackend) Sync(ctx context.Context, key string) error {
	if err := b.fail(ctx, "object-fence", key); err != nil {
		return err
	}
	if b, ok := b.Backend.(publication.DurabilityFence); ok {
		return b.Sync(ctx, key)
	}
	return nil
}

func TestRuntimeFatalOptionalBoundaries(t *testing.T) {
	for _, repair := range []bool{false, true} {
		for _, stage := range []string{"upload", "object-fence"} {
			for _, shutdown := range []string{"live", "joined", "pure"} {
				t.Run(stage+"/"+shutdown+map[bool]string{false: "/initial", true: "/repair"}[repair], func(t *testing.T) {
					dir, o, r := setup(t)
					o.EnableEnrichment = true
					q := open(t, dir, o)
					capture(t, q, r)
					base := newBackend(t)
					if err := q.Deliver(t.Context(), base, r.Identity); err != nil {
						t.Fatal(err)
					}
					enrichOnce(t, q, snapshotOptions("optional body"))
					m, _, err := q.latest(t.Context(), r.Identity)
					if err != nil {
						t.Fatal(err)
					}
					q.opts.EnableEnrichment = false

					key := m.Sources[1].Object.Key
					if repair {
						broken := &optionalBackend{Backend: base, key: key, stage: "upload"}
						broken.fail.Store(true)
						for range 3 {
							if err := q.Deliver(t.Context(), broken, r.Identity); err != nil {
								t.Fatal(err)
							}
						}
						var n int
						if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM optional_repairs`).Scan(&n); err != nil || n != 1 {
							t.Fatalf("repair not retained: %d %v", n, err)
						}
					} else if stage == "object-fence" {
						if err := q.Deliver(t.Context(), base, r.Identity); err != nil {
							t.Fatal(err)
						}
						if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET state='pending' WHERE sequence=?`, m.Sequence); err != nil {
							t.Fatal(err)
						}
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					b := &fatalBoundaryBackend{Backend: base, key: key, stage: stage, cause: publication.ErrDurability}
					if shutdown != "live" {
						b.cancel = cancel
						b.cause = context.Canceled
					}
					if shutdown == "joined" {
						b.cause = errors.Join(context.Canceled, publication.ErrDurability)
					}
					done := make(chan error, 1)
					go func() {
						done <- q.Run(ctx, b, RuntimeOptions{PollInterval: time.Millisecond, OperationTimeout: time.Second})
					}()
					select {
					case err := <-done:
						if shutdown == "pure" {
							if err != nil {
								t.Fatal(err)
							}
						} else if !errors.Is(err, publication.ErrDurability) {
							t.Fatalf("lost fatal cause: %v", err)
						}
					case <-time.After(5 * time.Second):
						cancel()
						<-done
						t.Fatal("Run swallowed boundary failure")
					}
					var state, code string
					query := `SELECT state,last_error FROM jobs WHERE sequence=?`
					arg := any(m.Sequence)
					if repair {
						query = `SELECT state,last_error FROM optional_repairs WHERE id=?`
						arg = repairID(r.Identity, m.Sources[1])
					}
					if err := q.db.QueryRowContext(t.Context(), query, arg).Scan(&state, &code); err != nil {
						t.Fatal(err)
					}
					// Reconciliation may concurrently supersede the old composite and
					// move its retained object into the repair worker. Whichever lane
					// encountered the injected failure must durably record its retry;
					// the failed composite must never receive a published ACK.
					if !repair && state == "superseded" && code == "" {
						if err := q.db.QueryRowContext(t.Context(), `SELECT state,last_error FROM optional_repairs WHERE id=?`, repairID(r.Identity, m.Sources[1])).Scan(&state, &code); err != nil {
							t.Fatal(err)
						}
					}
					if (state != "retry" && state != "superseded") || code == "" {
						t.Fatalf("failure bookkeeping: %s %s", state, code)
					}
				})
			}
		}
	}
}

func TestRuntimeFatalClassificationAndBookkeeping(t *testing.T) {
	q, r, m := optionalFixture(t)
	q.opts.EnableEnrichment = false
	cause := errors.Join(context.Canceled, &os.PathError{Op: "sync", Path: "synthetic", Err: syscall.EIO})
	if runtimeFatalCause(cause) == nil || runtimeFatalCause(errors.Join(context.DeadlineExceeded, publication.ErrDurability)) == nil {
		t.Fatal("lost local storage error")
	}
	for _, err := range []error{context.DeadlineExceeded, errors.New("SDK network failure"), ErrIntegrity, ErrDestination} {
		if runtimeFatalCause(err) != nil {
			t.Fatalf("unexpected fatal: %v", err)
		}
	}
	_, dbErr := q.db.ExecContext(t.Context(), "SELECT * FROM runtime_fatal_nonexistent_table")
	if dbErr == nil || runtimeFatalCause(errors.Join(context.Canceled, dbErr)) == nil {
		t.Fatal("typed DB failure not fatal")
	}
	bookkeeping, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancel()
	o := runtimeDefaults(RuntimeOptions{})
	for _, err := range []error{
		q.runtimeDeliveryFailure(bookkeeping, r.Identity, m.Sequence, 1, cause, o),
		q.saveOptionalRepairFailure(bookkeeping, repairID(r.Identity, m.Sources[1]), 1, cause, o),
	} {
		if !errors.Is(err, syscall.EIO) || !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("bookkeeping error replaced fatal cause: %v", err)
		}
	}
	if err := q.db.Close(); err != nil {
		t.Fatal(err)
	}
	err := q.runtimeDeliveryFailure(t.Context(), r.Identity, m.Sequence, 1, cause, runtimeDefaults(RuntimeOptions{}))
	if !errors.Is(err, syscall.EIO) || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost bookkeeping/cause join: %v", err)
	}
}

func TestRuntimeFatalNetworkRetryKeepsRawAlive(t *testing.T) {
	testRuntimeNetworkRetryRawACK(t, false)
}

func TestRuntimeNetworkRetryWaitsForNewestRawACK(t *testing.T) {
	testRuntimeNetworkRetryRawACK(t, true)
}

func testRuntimeNetworkRetryRawACK(t *testing.T, priorACK bool) {
	t.Helper()
	for _, cause := range []error{errors.New("ordinary network failure"), context.DeadlineExceeded} {
		dir, o, r := setup(t)
		o.EnableEnrichment = true
		o.Now = time.Now
		q := open(t, dir, o)
		capture(t, q, r)
		base := newBackend(t)
		if err := q.Deliver(t.Context(), base, r.Identity); err != nil {
			t.Fatal(err)
		}
		enrichOnce(t, q, snapshotOptions("optional body"))
		m, _, err := q.latest(t.Context(), r.Identity)
		if err != nil {
			t.Fatal(err)
		}
		q.opts.EnableEnrichment = false

		b := &fatalBoundaryBackend{Backend: base, key: m.Sources[1].Object.Key, stage: "upload", cause: cause, entered: make(chan struct{}), release: make(chan struct{}), attempts: make(chan struct{}, 8)}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- q.Run(ctx, b, RuntimeOptions{PollInterval: time.Millisecond, OperationTimeout: time.Second})
		}()
		select {
		case <-b.entered:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatal("optional upload never started")
		}
		releaseRaw := func() {}
		if priorACK {
			// Expire the original optional operation before the new capture.
			// Its withheld view must genuinely ACK, not merely be accepted.
			select {
			case <-b.attempts:
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("optional failure never recorded")
			}
			priorDeadline := time.After(5 * time.Second)
			priorTicker := time.NewTicker(time.Millisecond)
			for {
				var n int
				if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM jobs WHERE state='published' AND sequence>?`, m.Sequence).Scan(&n); err != nil {
					cancel()
					<-done
					priorTicker.Stop()
					t.Fatal(err)
				}
				if n > 0 {
					break
				}
				select {
				case <-priorTicker.C:
				case <-priorDeadline:
					cancel()
					<-done
					priorTicker.Stop()
					t.Fatal("prior withheld raw ACK missing")
				}
			}
			priorTicker.Stop()
			lane := q.deliveryLane(r.Identity, false)
			lane.Lock()
			var once sync.Once
			releaseRaw = func() { once.Do(lane.Unlock) }
			t.Cleanup(releaseRaw)
		}
		write(t, r.Path, "first\nnewest-network\n")
		newest := capture(t, q, r)
		// A normal optional transport outcome is withheld with durable repair, and
		// cannot prevent the raw view from reaching a genuine published ACK.
		deadline := time.After(5 * time.Second)
		ticker := time.NewTicker(time.Millisecond)
		published := false
		for !published {
			select {
			case err := <-done:
				cancel()
				ticker.Stop()
				t.Fatalf("network stopped Run: %v", err)
			case <-deadline:
				cancel()
				<-done
				ticker.Stop()
				t.Fatal("raw ACK blocked by network")
			case <-ticker.C:
				var n int
				if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM jobs WHERE state='published' AND sequence>=?`, newest.Sequence).Scan(&n); err != nil {
					cancel()
					<-done
					ticker.Stop()
					t.Fatal(err)
				}
				published = n > 0
				if !published {
					// The controlled case holds newest raw pending through this
					// first probe: an older withheld ACK must not end the wait.
					releaseRaw()
				}
			}
		}
		ticker.Stop()
		remote, err := publication.Read(t.Context(), base, r.Identity)
		if err != nil || remote.Sources[0].Object != newest.Sources[0].Object || remote.Sources[1].Withheld == nil {
			cancel()
			<-done
			t.Fatalf("wrong raw view: %v", err)
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("Run exited: %v", err)
		default:
		}
		close(b.release)
		for range 2 {
			select {
			case <-b.attempts:
			case err := <-done:
				cancel()
				t.Fatalf("Run stopped instead of retrying: %v", err)
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("no autonomous repair retry")
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var n int
		if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM optional_repairs WHERE state IN ('pending','retry')`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("retry lost: %d %v", n, err)
		}
	}
}

// cancellationDBConnector returns a driver error after cancellation occurs inside
// the query, so database/sql cannot short-circuit it before calling the driver.
// This models a canceled operation with a genuine independent DB failure leaf.
type cancellationDBConnector struct {
	cancel context.CancelFunc
	err    error
}

func (c cancellationDBConnector) Connect(context.Context) (driver.Conn, error) {
	return cancellationDBConn{c}, nil
}
func (c cancellationDBConnector) Driver() driver.Driver { return cancellationDBDriver{c} }

type cancellationDBDriver struct{ c cancellationDBConnector }

func (d cancellationDBDriver) Open(string) (driver.Conn, error) { return cancellationDBConn(d), nil }

type cancellationDBConn struct{ c cancellationDBConnector }

func (c cancellationDBConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c cancellationDBConn) Close() error { return nil }

func (c cancellationDBConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected begin")
}

func (c cancellationDBConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.c.cancel()
	return nil, c.c.err
}

func TestRuntimeFatalLoadEnrichmentCancellation(t *testing.T) {
	q, _, _ := optionalFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.loadEnrichmentResult(ctx, "missing"); !IsCallerCancellation(ctx, err) || errors.Is(err, ErrIntegrity) {
		t.Fatalf("pure cancellation became corruption: %v", err)
	}
	if _, err := q.loadEnrichmentResult(t.Context(), "missing"); !errors.Is(err, ErrIntegrity) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing result lost integrity: %v", err)
	}
	_, dbErr := q.db.ExecContext(t.Context(), "SELECT * FROM runtime_fatal_missing_table")
	if dbErr == nil {
		t.Fatal("expected typed SQLite error")
	}
	for _, cause := range []error{dbErr, errors.Join(context.Canceled, dbErr), errors.Join(context.Canceled, publication.ErrDurability), errors.Join(context.Canceled, ErrIntegrity)} {
		ctx, cancel := context.WithCancel(t.Context())
		original := q.db
		injected := sql.OpenDB(cancellationDBConnector{cancel: cancel, err: cause})
		q.db = injected
		_, err := q.loadEnrichmentResult(ctx, "synthetic")
		q.db = original
		cancel()
		if closeErr := injected.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err == nil || IsCallerCancellation(ctx, err) || !errors.Is(err, ErrIntegrity) || !errors.Is(err, cause) {
			t.Fatalf("lost genuine DB tree: %v", err)
		}
	}
}
