package sessioncapture

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

type shutdownRepairBackend struct {
	publication.Backend
	fail func(context.Context) error
}

func (b shutdownRepairBackend) PutFile(ctx context.Context, _, _ string) error { return b.fail(ctx) }

func TestOptionalWorkersShutdownBudget(t *testing.T) {
	for _, lane := range []string{"enrichment", "repair"} {
		for _, kind := range []string{"shutdown", "deadline", "joined-integrity", "joined-durability", "live-cancellation"} {
			t.Run(lane+"/"+kind, func(t *testing.T) {
				dir, o, r := setup(t)
				q := open(t, dir, o)
				if err := q.InitRuntime(t.Context()); err != nil {
					t.Fatal(err)
				}
				m := capture(t, q, r)
				_, es, err := q.latest(t.Context(), r.Identity)
				if err != nil {
					t.Fatal(err)
				}
				table, id := "enrichment_work", "shutdown-test"
				if lane == "enrichment" {
					spec := EnrichmentSpec{Kind: "snapshot", Identity: r.Identity, Source: SourceCoordinate{SourceID: r.SourceID, Path: r.Path}}
					body, err := json.Marshal(spec)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := q.db.ExecContext(t.Context(), `INSERT INTO enrichment_work(id,session,spec,contract,attempts) VALUES(?,?,?,?,7)`, id, r.Identity.BaseKey(), body, EnrichmentContract); err != nil {
						t.Fatal(err)
					}
				} else {
					table = "optional_repairs"
					if err := q.persistOptionalRepair(t.Context(), r.Identity, m.Sources[0], es[0], "upload"); err != nil {
						t.Fatal(err)
					}
					id = repairID(r.Identity, m.Sources[0])
					if _, err := q.db.ExecContext(t.Context(), `UPDATE optional_repairs SET attempts=7`); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if kind == "deadline" {
					var expire context.CancelFunc
					ctx, expire = context.WithTimeout(ctx, time.Second)
					defer expire()
				}
				fail := func(c context.Context) error {
					if kind == "deadline" {
						<-c.Done()
						return c.Err()
					}
					if kind == "live-cancellation" {
						return context.Canceled
					}
					cancel()
					switch kind {
					case "joined-integrity":
						return errors.Join(c.Err(), ErrIntegrity)
					case "joined-durability":
						return errors.Join(c.Err(), publication.ErrDurability)
					}
					return c.Err()
				}
				opts := runtimeDefaults(RuntimeOptions{MaxAttempts: 8, OperationTimeout: time.Second, Enrich: func(c context.Context, _ EnrichmentSpec) (EnrichmentArtifact, error) {
					return EnrichmentArtifact{}, fail(c)
				}})
				if lane == "enrichment" {
					_, err = q.enrichmentNext(ctx, opts)
				} else {
					_, err = q.optionalRepairNext(ctx, shutdownRepairBackend{newBackend(t), fail}, opts)
				}
				if kind == "joined-durability" && !errors.Is(err, publication.ErrDurability) {
					t.Fatalf("lost fatal cause: %v", err)
				}
				var state string
				var attempts int
				var next int64
				if err := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,next_retry FROM `+table+` WHERE id=?`, id).Scan(&state, &attempts, &next); err != nil {
					t.Fatal(err)
				}
				if kind != "shutdown" {
					if state != "quarantined" || attempts != 8 || next == 0 {
						t.Fatalf("genuine failure refunded: %s %d %d", state, attempts, next)
					}
					return
				}
				if state != "retry" || attempts != 7 || next != 0 {
					t.Fatalf("shutdown spent terminal budget: %s %d %d", state, attempts, next)
				}
				if err := q.Close(); err != nil {
					t.Fatal(err)
				}
				q = open(t, dir, o)
				if err := q.InitRuntime(t.Context()); err != nil {
					t.Fatal(err)
				}
				// No hint/replay/new hook: the persisted work is immediately eligible.
				var did bool
				if lane == "enrichment" {
					did, err = q.enrichmentNext(t.Context(), snapshotOptions("restarted result"))
				} else {
					did, err = q.optionalRepairNext(t.Context(), newBackend(t), opts)
				}
				if err != nil || !did {
					t.Fatalf("restart retry: %v %v", did, err)
				}
				if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM `+table+` WHERE id=?`, id).Scan(&state); err != nil {
					t.Fatal(err)
				}
				want := "attached"
				if lane == "repair" {
					want = "repaired"
				}
				if state != want {
					t.Fatalf("retry state %s want %s", state, want)
				}
			})
		}
	}
}

type enrichmentBoundaryContext struct {
	context.Context
	cancel   context.CancelFunc
	boundary string
}

func (c *enrichmentBoundaryContext) endAtBoundary() {
	pcs := make([]uintptr, 24)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if strings.HasSuffix(f.Function, ".(*Queue)."+c.boundary) {
			c.cancel()
			break
		}
		if !more {
			break
		}
	}
}
func (c *enrichmentBoundaryContext) Err() error { c.endAtBoundary(); return c.Context.Err() }
func (c *enrichmentBoundaryContext) Done() <-chan struct{} {
	c.endAtBoundary()
	return c.Context.Done()
}

func TestEnrichmentShutdownKeepsSavedResult(t *testing.T) {
	for _, boundary := range []string{"policy", "loadEnrichmentResult", "storeEnrichmentResult", "attachEnrichment"} {
		t.Run(boundary, func(t *testing.T) {
			dir, o, r := setup(t)
			o.EnableEnrichment = true
			var cancel context.CancelFunc
			o.CheckPolicy = func(context.Context, Request) error {
				if boundary == "policy" && cancel != nil {
					cancel()
					return context.Canceled
				}
				return nil
			}
			q := open(t, dir, o)
			capture(t, q, r)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			var id string
			var body []byte
			if err := q.db.QueryRowContext(t.Context(), `SELECT id,spec FROM enrichment_work LIMIT 1`).Scan(&id, &body); err != nil {
				t.Fatal(err)
			}
			var spec EnrichmentSpec
			if err := json.Unmarshal(body, &spec); err != nil {
				t.Fatal(err)
			}
			result, err := q.buildEnrichment(t.Context(), spec, snapshotOptions("saved immutable result"))
			if err != nil {
				t.Fatal(err)
			}
			resultID, err := q.storeEnrichmentResult(t.Context(), result)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := q.db.ExecContext(t.Context(), `UPDATE enrichment_work SET state='ready',result=?,attempts=7 WHERE id=?`, []byte(resultID), id); err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			cancel = stop
			operation := ctx
			if boundary != "policy" {
				operation = &enrichmentBoundaryContext{Context: ctx, cancel: stop, boundary: boundary}
			}
			_, err = q.enrichmentNext(operation, runtimeDefaults(RuntimeOptions{MaxAttempts: 8}))
			cancel = nil
			if ctx.Err() != context.Canceled {
				t.Fatalf("boundary %s did not cancel operation", boundary)
			}
			var state string
			var attempts int
			var saved []byte
			var next int64
			if e := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,result,next_retry FROM enrichment_work WHERE id=?`, id).Scan(&state, &attempts, &saved, &next); e != nil {
				t.Fatal(e)
			}
			if state != "ready" || attempts != 7 || string(saved) != resultID || next != 0 {
				t.Fatalf("lost ready result: state=%s attempts=%d saved=%s next=%d err=%v", state, attempts, saved, next, err)
			}
			if err := q.Close(); err != nil {
				t.Fatal(err)
			}
			q = open(t, dir, o)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			// No callback is available: only the preserved result can complete this work.
			enrichOnce(t, q, runtimeDefaults(RuntimeOptions{}))
			if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM enrichment_work WHERE id=?`, id).Scan(&state); err != nil || state != "attached" {
				t.Fatalf("saved result retry: %s %v", state, err)
			}
		})
	}
}

func TestRepairShutdownKeepsVerifiedAttachment(t *testing.T) {
	dir, o, r := setup(t)
	var cancel context.CancelFunc
	o.Fault = func(stage string) error {
		if stage == "optional-repair-verified" && cancel != nil {
			cancel()
			return context.Canceled
		}
		return nil
	}
	q := open(t, dir, o)
	m := capture(t, q, r)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.persistOptionalRepair(t.Context(), r.Identity, m.Sources[0], es[0], "upload"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE optional_repairs SET attempts=7`); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	cancel = stop
	_, err = q.optionalRepairNext(ctx, newBackend(t), runtimeDefaults(RuntimeOptions{MaxAttempts: 8}))
	cancel = nil
	var state string
	var attempts int
	var next int64
	if e := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,next_retry FROM optional_repairs LIMIT 1`).Scan(&state, &attempts, &next); e != nil {
		t.Fatal(e)
	}
	if state != "verified_attachment_pending" || attempts != 7 || next != 0 {
		t.Fatalf("lost verified state: %s %d %d err=%v", state, attempts, next, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A nil backend is intentional: durable verification must not be repeated.
	did, err := q.optionalRepairNext(t.Context(), nil, runtimeDefaults(RuntimeOptions{}))
	if !did || err != nil {
		t.Fatalf("verified restart: %v %v", did, err)
	}
	if err := q.db.QueryRowContext(t.Context(), `SELECT state,attempts FROM optional_repairs LIMIT 1`).Scan(&state, &attempts); err != nil || state != "repaired" || attempts != 7 {
		t.Fatalf("attachment retry: %s %d %v", state, attempts, err)
	}
}

func TestEnrichmentSourceAbsenceRemainsIsolated(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	q := open(t, dir, o)
	capture(t, q, r)
	opts := runtimeDefaults(RuntimeOptions{Enrich: func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error) {
		return EnrichmentArtifact{}, &os.PathError{Op: "open", Path: "synthetic-absent", Err: os.ErrNotExist}
	}})
	enrichOnce(t, q, opts)
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); err != nil {
		t.Fatalf("optional source absence stopped raw delivery: %v", err)
	}
	var state string
	if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM enrichment_work LIMIT 1`).Scan(&state); err != nil || state != "retry" {
		t.Fatalf("optional absence not isolated: %s %v", state, err)
	}
}

func TestShutdownRefundDoesNotChangeAnotherClaim(t *testing.T) {
	for _, lane := range []string{"enrichment", "repair"} {
		for _, transition := range []string{"quarantined", "working", "delivering"} {
			if lane == "enrichment" && transition == "delivering" {
				continue
			}
			t.Run(lane+"/"+transition, func(t *testing.T) {
				dir, o, r := setup(t)
				q := open(t, dir, o)
				m := capture(t, q, r)
				if err := q.InitRuntime(t.Context()); err != nil {
					t.Fatal(err)
				}
				_, es, err := q.latest(t.Context(), r.Identity)
				if err != nil {
					t.Fatal(err)
				}
				table, id := "enrichment_work", "ownership"
				if lane == "enrichment" {
					body, err := json.Marshal(EnrichmentSpec{Kind: "snapshot", Identity: r.Identity})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := q.db.ExecContext(t.Context(), `INSERT INTO enrichment_work(id,session,spec,contract,attempts) VALUES(?,?,?,?,7)`, id, r.Identity.BaseKey(), body, EnrichmentContract); err != nil {
						t.Fatal(err)
					}
				} else {
					table = "optional_repairs"
					id = repairID(r.Identity, m.Sources[0])
					if err := q.persistOptionalRepair(t.Context(), r.Identity, m.Sources[0], es[0], "upload"); err != nil {
						t.Fatal(err)
					}
					if _, err := q.db.ExecContext(t.Context(), `UPDATE optional_repairs SET attempts=7`); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				expected := 8
				if transition == "working" {
					expected = 9
				}
				fail := func(c context.Context) error {
					// Model delivery taking ownership, a terminal transition, or a later claim
					// before the original interrupted invocation reaches its deferred release.
					if _, err := q.db.ExecContext(c, `UPDATE `+table+` SET state=?,attempts=?,next_retry=123,last_error='new-owner' WHERE id=?`, transition, expected, id); err != nil {
						t.Fatal(err)
					}
					cancel()
					return context.Canceled
				}
				opts := runtimeDefaults(RuntimeOptions{Enrich: func(c context.Context, _ EnrichmentSpec) (EnrichmentArtifact, error) {
					return EnrichmentArtifact{}, fail(c)
				}})
				if lane == "enrichment" {
					_, err = q.enrichmentNext(ctx, opts)
				} else {
					_, err = q.optionalRepairNext(ctx, shutdownRepairBackend{newBackend(t), fail}, opts)
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost shutdown: %v", err)
				}
				var state, code string
				var attempts int
				var next int64
				if err := q.db.QueryRowContext(t.Context(), `SELECT state,attempts,next_retry,last_error FROM `+table+` WHERE id=?`, id).Scan(&state, &attempts, &next, &code); err != nil {
					t.Fatal(err)
				}
				if state != transition || attempts != expected || next != 123 || code != "new-owner" {
					t.Fatalf("changed another owner: %s %d %d %s", state, attempts, next, code)
				}
			})
		}
	}
}
