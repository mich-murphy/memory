package sessioncapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tripCtx ends after a fixed number of context checks made by contextReader.Read
// once armed. Other callers (database/sql, locks) see the underlying context until
// it trips, so the end lands deterministically inside a chosen read phase.
type tripCtx struct {
	context.Context
	cause   error
	limit   int64
	armed   atomic.Bool
	reads   atomic.Int64
	tripped atomic.Bool
	onTrip  func()
}

func (c *tripCtx) Err() error {
	if c.tripped.Load() {
		return c.cause
	}
	if !c.armed.Load() {
		return c.Context.Err()
	}
	pc, _, _, ok := runtime.Caller(1)
	if ok && strings.Contains(runtime.FuncForPC(pc).Name(), "contextReader") {
		if c.reads.Add(1) > c.limit {
			c.tripped.Store(true)
			if c.onTrip != nil {
				c.onTrip()
			}
			return c.cause
		}
	}
	return c.Context.Err()
}

// verifyReads counts the context checks one successful verify performs.
func verifyReads(t *testing.T, q *Queue, req Request) int64 {
	t.Helper()
	m := capture(t, q, req)
	c := &tripCtx{Context: t.Context(), limit: 1 << 40}
	c.armed.Store(true)
	if err := q.verify(c, m.Sources[0].Object); err != nil {
		t.Fatal(err)
	}
	return c.reads.Load()
}

func TestVerifyContextEndDuringDecodeIsNotIntegrity(t *testing.T) {
	for name, cause := range map[string]error{"canceled": context.Canceled, "deadline": context.DeadlineExceeded} {
		t.Run(name, func(t *testing.T) {
			dir, o, req := setup(t)
			q := open(t, dir, o)
			n := verifyReads(t, q, req)
			obj := capture(t, q, req).Sources[0].Object
			// The final check precedes the decode phase's EOF read.
			c := &tripCtx{Context: t.Context(), cause: cause, limit: n - 1}
			c.armed.Store(true)
			err := q.verify(c, obj)
			if !c.tripped.Load() {
				t.Fatalf("context never ended in %d reads", n)
			}
			if err != cause { //nolint:errorlint // The context error must be returned unwrapped.
				t.Fatalf("got %v want unwrapped %v", err, cause)
			}
			if errors.Is(err, ErrIntegrity) {
				t.Fatal("context end labelled integrity")
			}
			if name == "deadline" && classify(err, nil) != FailureTimeout {
				t.Fatalf("class %s", classify(err, nil))
			}
			if name == "canceled" && !pureCancellation(err, context.Canceled) {
				t.Fatal("cancellation not pure")
			}
		})
	}
}

func TestVerifyCorruptPayloadWithLiveContextIsIntegrity(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	obj := capture(t, q, req).Sources[0].Object
	good, err := os.ReadFile(q.payload(obj))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("encoded hash", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[len(bad)-1] ^= 0xff
		if err := os.WriteFile(q.payload(obj), bad, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(q.payload(obj), good, 0o600) })
		if err := q.verify(t.Context(), obj); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("decoded hash", func(t *testing.T) {
		wrong := obj
		wrong.SHA256 = strings.Repeat("0", 64)
		if err := q.verify(t.Context(), wrong); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("decoded size", func(t *testing.T) {
		wrong := obj
		wrong.Size++
		if err := q.verify(t.Context(), wrong); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("corrupt zstd with matching encoded hash", func(t *testing.T) {
		junk := []byte("not a zstd frame, but a self-consistent file")
		d := sha256.Sum256(junk)
		fake := obj
		fake.EncodedSHA256 = hex.EncodeToString(d[:])
		fake.EncodedSize = int64(len(junk))
		if err := os.WriteFile(q.payload(fake), junk, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := q.verify(t.Context(), fake); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%v", err)
		}
	})
}

func TestShutdownDuringDependencyDecodeRestoresAttempts(t *testing.T) {
	dir, o, req := setup(t)
	var tc atomic.Pointer[tripCtx]
	var live atomic.Bool
	o.CheckPolicy = func(context.Context, Request) error {
		if c := tc.Load(); live.Load() && c != nil {
			c.armed.Store(true)
		}
		return nil
	}
	q := open(t, dir, o)
	n := verifyReads(t, q, req)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `UPDATE jobs SET attempts=7`); err != nil {
		t.Fatal(err)
	}
	run, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	op, cancel := context.WithTimeout(run, time.Minute)
	defer cancel()
	c := &tripCtx{Context: op, cause: context.Canceled, limit: n - 1, onTrip: shutdown}
	tc.Store(c)
	live.Store(true)
	_, err := q.deliverDueWork(c, newBackend(t), req.Identity, runtimeDefaults(RuntimeOptions{}))
	if !c.tripped.Load() {
		t.Fatalf("shutdown never hit dependency verification (%d reads): %v", c.reads.Load(), err)
	}
	if runtimeStopError(run, err) != nil {
		t.Fatalf("shutdown must be a clean stop: %v", err)
	}
	state, attempts, next := jobRow(t, q)
	var reason string
	if e := q.db.QueryRowContext(t.Context(), `SELECT last_error FROM jobs LIMIT 1`).Scan(&reason); e != nil {
		t.Fatal(e)
	}
	if attempts != 7 || state != "retry" || next != 0 || reason != "shutdown" || quarantineCount(t, q) != 0 {
		t.Fatalf("state=%s attempts=%d next=%d reason=%s", state, attempts, next, reason)
	}
}
