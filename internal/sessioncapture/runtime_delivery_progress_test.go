package sessioncapture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// Only synthetic fixture bytes enter this backend. Counts include attempted puts
// and publications, not just changes in the final object map.
type progressMemoryBackend struct {
	objects     map[string][]byte
	puts, swaps int
}

func (b *progressMemoryBackend) Open(_ context.Context, key string) (io.ReadCloser, error) {
	body, ok := b.objects[key]
	if !ok {
		return nil, publication.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (b *progressMemoryBackend) PutFile(_ context.Context, key, path string) error {
	b.puts++
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b.objects[key] = body
	return nil
}

func (b *progressMemoryBackend) Current(_ context.Context, key string) ([]byte, string, error) {
	body, ok := b.objects[key]
	if !ok {
		return nil, "", publication.ErrNotFound
	}
	return bytes.Clone(body), string(body), nil
}

func (b *progressMemoryBackend) CompareAndSwap(_ context.Context, key, token string, body []byte) error {
	b.swaps++
	if string(b.objects[key]) != token {
		return publication.ErrConflict
	}
	b.objects[key] = bytes.Clone(body)
	return nil
}

// A real wrong-lane turn seeds the adverse scan position. Subsequent worker
// turns must drain accepted work without resetting either cursor or sleeping.
func TestRuntimeDeliveryProgressLaneCursors(t *testing.T) {
	for _, optional := range []bool{false, true} {
		for _, correctFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("optional=%v/correctFirst=%v", optional, correctFirst), func(t *testing.T) {
				dir, o, req := setup(t)
				o.EnableEnrichment = optional
				acknowledgements := 0
				o.Fault = func(point string) error {
					if point == "ack-before-commit" {
						acknowledgements++
					}
					return nil
				}
				q := open(t, dir, o)
				capture(t, q, req)
				if err := q.InitRuntime(t.Context()); err != nil {
					t.Fatal(err)
				}
				b := &progressMemoryBackend{objects: make(map[string][]byte)}
				if err := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
					t.Fatal(err)
				}
				if optional {
					if err := q.Deliver(t.Context(), b, req.Identity); err != nil {
						t.Fatal(err)
					}
					enrichOnce(t, q, snapshotOptions("synthetic optional"))
				}
				want, _, err := q.latest(t.Context(), req.Identity)
				if err != nil {
					t.Fatal(err)
				}
				head, err := q.optionalDeliveryHead(t.Context(), req.Identity)
				if err != nil || head != optional {
					t.Fatalf("wrong head: %v %v", head, err)
				}
				acknowledgements, b.puts, b.swaps = 0, 0, 0
				opts := runtimeDefaults(RuntimeOptions{})
				turn := func(lane bool) bool {
					t.Helper()
					puts, swaps := b.puts, b.swaps
					did, err := q.deliveryLaneNext(t.Context(), b, opts, lane)
					if err != nil {
						t.Fatal(err)
					}
					var claims int
					if err := q.db.QueryRowContext(t.Context(), "SELECT sum(claim) FROM runtime_sessions").Scan(&claims); err != nil {
						t.Fatal(err)
					}
					if claims != 0 {
						t.Fatalf("lane=%v retained %d claims", lane, claims)
					}
					if lane != optional && (did || b.puts != puts || b.swaps != swaps) {
						t.Fatal("wrong lane attempted publication")
					}
					return did
				}
				turn(!optional) // Legitimate selection/rejection, not a synthetic cursor write.
				completed := 0
				for range 8 {
					if !correctFirst {
						turn(!optional)
					}
					if turn(optional) {
						completed++
					}
					if correctFirst {
						turn(!optional)
					}
				}
				if completed != 1 || acknowledgements != 1 || b.puts == 0 || b.swaps == 0 {
					t.Fatalf("accepted head starved or duplicated: completed=%d acknowledgements=%d puts=%d publications=%d", completed, acknowledgements, b.puts, b.swaps)
				}
				status, err := q.Status(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, job := range status {
					if job.State != "published" || job.LastError != "" {
						t.Fatalf("accepted work not drained: %+v", job)
					}
				}
				got, err := publication.Read(t.Context(), b, req.Identity)
				if err != nil || got.PublicationID != want.PublicationID || got.Sequence != want.Sequence || got.Identity != req.Identity {
					t.Fatalf("wrong publication: got=%+v want=%+v err=%v", got, want, err)
				}
				puts, swaps := b.puts, b.swaps
				for range 4 {
					if turn(false) || turn(true) {
						t.Fatal("drained queue reported more work")
					}
				}
				if acknowledgements != 1 || b.puts != puts || b.swaps != swaps {
					t.Fatal("drained queue republished work")
				}
			})
		}
	}
}

func TestRuntimeDeliveryProgressIdleScheduling(t *testing.T) {
	for _, optional := range []bool{false, true} {
		for _, idle := range []string{"busy", "paused"} {
			t.Run(fmt.Sprintf("optional=%v/%s", optional, idle), func(t *testing.T) {
				dir, o, req := setup(t)
				o.EnableEnrichment = optional
				attempts := 0
				o.Fault = func(point string) error {
					if point == "upload-before" {
						attempts++
					}
					return nil
				}
				q := open(t, dir, o)
				capture(t, q, req)
				if err := q.InitRuntime(t.Context()); err != nil {
					t.Fatal(err)
				}
				b := &progressMemoryBackend{objects: make(map[string][]byte)}
				if err := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
					t.Fatal(err)
				}
				if optional {
					if err := q.Deliver(t.Context(), b, req.Identity); err != nil {
						t.Fatal(err)
					}
					enrichOnce(t, q, snapshotOptions("synthetic optional"))
				}
				attempts = 0
				head, err := q.optionalDeliveryHead(t.Context(), req.Identity)
				if err != nil || head != optional {
					t.Fatalf("wrong head: %v %v", head, err)
				}
				if idle == "busy" {
					lock := q.deliveryLane(req.Identity, optional)
					lock.Lock()
					defer lock.Unlock()
				} else if err := q.pauseDestination(t.Context()); err != nil {
					t.Fatal(err)
				}
				before, err := q.Status(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				b.puts, b.swaps = 0, 0
				spurious := 0
				visited := 0
				for range 10 {
					// claimSession uses a bounded cursor; reset it so every turn visits
					// this accepted, due session rather than alternating with an empty page.
					lane := 0
					if optional {
						lane = 1
					}
					q.runtimeLocal().sessionCursors[lane] = 0
					did, err := q.deliveryLaneNext(t.Context(), b, runtimeDefaults(RuntimeOptions{}), optional)
					if err != nil {
						t.Fatal(err)
					}
					visited++
					if did {
						spurious++
					}
				}
				after, err := q.Status(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				var claims int
				if err := q.db.QueryRowContext(t.Context(), "SELECT sum(claim) FROM runtime_sessions").Scan(&claims); err != nil {
					t.Fatal(err)
				}
				t.Logf("visited=%d spurious=%d attempts=%d puts=%d publications=%d claims=%d state_unchanged=%v", visited, spurious, attempts, b.puts, b.swaps, claims, reflect.DeepEqual(before, after))
				if spurious != 0 || attempts != 0 || b.puts != 0 || b.swaps != 0 || claims != 0 || !reflect.DeepEqual(before, after) {
					t.Errorf("idle delivery must report no work without side effects")
				}
				// Exercise the actual worker with the scheduler's measured result. Virtual
				// time proves it waits for its existing poll, without a wall-clock sleep or
				// a new deadline. A broken busy loop cancels itself after ten calls.
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					var calls atomic.Int32
					done := make(chan error, 1)
					go func() {
						done <- q.runtimeWorker(ctx, runtimeDefaults(RuntimeOptions{}), func(context.Context) (bool, error) {
							n := calls.Add(1)
							if n == 10 {
								cancel()
							}
							return spurious != 0, nil
						})
					}()
					synctest.Wait()
					if n := calls.Load(); n != 1 {
						t.Errorf("idle worker calls before poll=%d, want 1", n)
					}
					time.Sleep(100 * time.Millisecond)
					synctest.Wait()
					if n := calls.Load(); n != 2 {
						t.Errorf("idle worker calls after poll=%d, want 2", n)
					}
					cancel()
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				})
			})
		}
	}
}

// Simulate state changes after scheduler selection without timing-dependent SQL
// races: the delivery helper must recheck all these conditions and report idle.
func TestRuntimeDeliveryProgressRechecks(t *testing.T) {
	for _, state := range []string{"paused", "noRows", "quarantined", "notDue", "success", "retry", "fatal"} {
		t.Run(state, func(t *testing.T) {
			dir, o, req := setup(t)
			q := open(t, dir, o)
			capture(t, q, req)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			id, identity, err := q.claimSession(t.Context(), false)
			if err != nil || id == 0 {
				t.Fatalf("selection: %d %v", id, err)
			}
			if _, err := q.db.ExecContext(t.Context(), "UPDATE runtime_sessions SET claim=0 WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			b := &progressMemoryBackend{objects: make(map[string][]byte)}
			if err := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
				t.Fatal(err)
			}
			b.swaps = 0
			var backend publication.Backend = b
			want := false
			switch state {
			case "paused":
				if err := q.pauseDestination(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "noRows":
				if _, err := q.db.ExecContext(t.Context(), "UPDATE jobs SET state='published'"); err != nil {
					t.Fatal(err)
				}
			case "quarantined":
				if _, err := q.db.ExecContext(t.Context(), "UPDATE jobs SET state='quarantined'"); err != nil {
					t.Fatal(err)
				}
			case "notDue":
				if _, err := q.db.ExecContext(t.Context(), "UPDATE jobs SET next_retry=?", q.opts.Now().Add(time.Hour).UnixNano()); err != nil {
					t.Fatal(err)
				}
			case "success":
				want = true
			case "retry":
				want = true
				backend = runtimeBrokenBackend{Backend: b, err: errors.New("synthetic network failure")}
			case "fatal":
				want = true
				backend = runtimeBrokenBackend{Backend: b, err: publication.ErrDurability}
			}
			before, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			did, err := q.deliverDueWork(t.Context(), backend, identity, runtimeDefaults(RuntimeOptions{}))
			if did != want || (state == "fatal" && !errors.Is(err, publication.ErrDurability)) || (state != "fatal" && err != nil) {
				t.Fatalf("did=%v want=%v err=%v", did, want, err)
			}
			after, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !want && (!reflect.DeepEqual(before, after) || b.puts != 0 || b.swaps != 0) {
				t.Fatal("idle helper mutated state or backend")
			}
			if want && after[0].Attempts != before[0].Attempts+1 {
				t.Fatal("attempt not reached")
			}
		})
	}
}
