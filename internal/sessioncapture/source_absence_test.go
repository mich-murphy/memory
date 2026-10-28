package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSourceAbsenceBoundaryAndMixedFatal(t *testing.T) {
	missing := &os.PathError{Op: "lstat", Path: "synthetic-source", Err: syscall.ENOENT}
	absent := originalSourceError(fmt.Errorf("resolve: %w", missing))
	if !onlySourceAbsence(absent) || !errors.Is(absent, os.ErrNotExist) || runtimeFatalCause(absent) != nil {
		t.Fatal("source absence not isolated")
	}
	for _, fatal := range []error{missing, &os.PathError{Op: "sync", Path: "synthetic-spool", Err: syscall.EIO}, &os.PathError{Op: "open", Path: "synthetic-policy", Err: syscall.ENOENT}} {
		if runtimeFatalCause(fatal) == nil {
			t.Fatal("unclassified path error lost")
		}
		joined := errors.Join(absent, fatal)
		if onlySourceAbsence(joined) || runtimeFatalCause(joined) == nil {
			t.Fatal("mixed fatal tree hidden")
		}
		if onlySourceAbsence(originalSourceError(errors.Join(missing, fatal))) || runtimeFatalCause(originalSourceError(errors.Join(missing, fatal))) == nil {
			t.Fatal("tagged mixed error")
		}
	}
	if onlySourceAbsence(originalResolvedSourceError(fmt.Errorf("resolve: %w", syscall.ENOENT))) {
		t.Fatal("bare resolution errno is not proof of original-source absence")
	}
	if !onlySourceAbsence(originalResolvedSourceError(fmt.Errorf("resolve: %w", missing))) {
		t.Fatal("missing resolved source not classified")
	}
}

func TestStableObservationTagsOnlyOriginalPathLookup(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	source, err := q.checkObserved(t.Context(), evidence{Request: r}, false)
	if err != nil {
		t.Fatal(err)
	}
	f, err := source.open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // Read-only source.
	before, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.Path); err != nil {
		t.Fatal(err)
	}
	// The pinned descriptor still contains the original bytes, but path
	// re-resolution cannot authorize a second observation.
	absent := stableObservationSource(t.Context(), f, source.Path, before, before.Size(), "", true)
	if !onlySourceAbsence(absent) || runtimeFatalCause(absent) != nil {
		t.Fatalf("original source removal: %v", absent)
	}
	private := stableObservationSource(t.Context(), f, source.Path, before, before.Size(), "", false)
	if onlySourceAbsence(private) || runtimeFatalCause(private) == nil {
		t.Fatalf("private prepared output removal: %v", private)
	}
}

// Run must remain alive, publish B, record A absent once, and retain A's
// previously captured bytes. Repeated worker turns must not reset/retry A.
func TestSourceGoneRunAllowsHealthySession(t *testing.T) {
	dir, o, a := setup(t)
	q := open(t, dir, o)
	old := capture(t, q, a)
	if err := os.Remove(a.Path); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Identity.SessionID = "healthy"
	b.SourceID = "healthy"
	b.Path = filepath.Join(filepath.Dir(a.Path), "healthy.jsonl")
	write(t, b.Path, "healthy synthetic source\n")
	for _, r := range []Request{a, b} {
		if err := q.Hint(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	backend := newBackend(t)
	go func() {
		done <- q.Run(ctx, backend, RuntimeOptions{PollInterval: time.Millisecond, OperationTimeout: time.Second, CaptureWorkers: 1, DeliveryWorkers: 1})
	}()
	// Always join before Queue cleanup, including failed assertions.
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not join")
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			var absent, published int
			if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM runtime_sources WHERE state='absent' AND last_error='absent' AND attempts=1`).Scan(&absent); err != nil {
				t.Fatal(err)
			}
			if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM jobs WHERE session=? AND state='published'`, b.Identity.BaseKey()).Scan(&published); err != nil {
				t.Fatal(err)
			}
			if absent == 1 && published == 1 {
				for range 20 {
					if did, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); did || err != nil {
						t.Fatalf("absent source retried: %v %v", did, err)
					}
				}
				retained, _, err := q.latest(t.Context(), a.Identity)
				if err != nil || retained.PublicationID != old.PublicationID {
					t.Fatal("prior evidence changed", err)
				}
				if err := q.verify(t.Context(), old.Sources[0].Object); err != nil {
					t.Fatal("prior bytes lost", err)
				}
				return
			}
		case err := <-done:
			done <- err
			t.Fatalf("Run stopped before healthy progress: %v", err)
		case <-deadline.C:
			t.Fatal("healthy session did not publish alongside absent source")
		}
	}
}

func TestMissingPolicyAndSpoolRemainFatal(t *testing.T) {
	for _, kind := range []string{"policy", "spool"} {
		t.Run(kind, func(t *testing.T) {
			dir, o, r := setup(t)
			q := open(t, dir, o)
			if err := q.Hint(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			missing := filepath.Join(filepath.Dir(dir), "missing-policy")
			if kind == "policy" {
				q.opts.CheckPolicy = func(context.Context, Request) error { _, err := os.Stat(missing); return err }
			} else {
				// Move only synthetic test state; retained payload evidence remains intact.
				if err := os.Rename(filepath.Join(dir, "payloads"), filepath.Join(dir, "payloads-preserved")); err != nil {
					t.Fatal(err)
				}
			}
			_, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{}))
			if kind == "policy" && err != nil && strings.Contains(err.Error(), missing) {
				t.Fatal("private policy diagnostic exposed")
			}
			if err == nil || runtimeFatalCause(err) == nil || onlySourceAbsence(err) {
				t.Fatalf("missing %s not fatal: %v", kind, err)
			}
			var absent int
			if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM runtime_sources WHERE state='absent'`).Scan(&absent); err != nil || absent != 0 {
				t.Fatal("storage failure recorded as source absence", err)
			}
		})
	}
}

func TestSourceDisappearsAfterAuthorization(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	source, err := q.checkObserved(t.Context(), evidence{Request: r}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.Path); err != nil {
		t.Fatal(err)
	}
	_, err = source.open(t.Context())
	if !onlySourceAbsence(err) {
		t.Fatalf("authorized source open: %v", err)
	}
	// The same pathname missing from a private-output read must remain fatal.
	source.original = false
	_, err = source.open(t.Context())
	if runtimeFatalCause(err) == nil {
		t.Fatal("private output ENOENT not fatal")
	}
}

func TestExcludedCallbackPreservesSanitizedFatalClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
	}{
		{"pure", nil},
		{"missing-policy", &os.PathError{Op: "open", Path: "synthetic-private-policy-diagnostic", Err: syscall.ENOENT}},
		{"storage-io", &os.PathError{Op: "sync", Path: "synthetic-private-storage-diagnostic", Err: syscall.EIO}},
		{"cancellation", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, options, request := setup(t)
			options.CheckPolicy = func(context.Context, Request) error { return errors.Join(ErrSessionExcluded, tc.cause) }
			q := open(t, dir, options)
			// Exercise the actual capture step, not only the classifier: exclusion must
			// remain fail-closed, and mixed fatal causes must reach the runtime worker.
			if err := q.Hint(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			_, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{}))
			callbackErr := q.checkCallback(t.Context(), request)
			if !errors.Is(callbackErr, ErrPolicy) || !errors.Is(callbackErr, ErrSessionExcluded) {
				t.Fatal("exclusion markers lost")
			}
			_, marked := errors.AsType[*policyStorageFailure](callbackErr)
			fatal := tc.cause != nil
			if marked != fatal || (runtimeFatalCause(callbackErr) != nil) != fatal || (runtimeFatalCause(err) != nil) != fatal {
				t.Fatalf("fatal classification mismatch: callback=%v step=%v", callbackErr, err)
			}
			if strings.Contains(callbackErr.Error(), "synthetic-private") || (err != nil && strings.Contains(err.Error(), "synthetic-private")) {
				t.Fatal("private policy diagnostic exposed")
			}
			if pureCancellation(callbackErr, context.Canceled) {
				t.Fatal("mixed exclusion became clean cancellation")
			}
			jobs, statusErr := q.Status(t.Context())
			if statusErr != nil || len(jobs) != 0 {
				t.Fatal("excluded callback accepted source bytes", statusErr)
			}
		})
	}
}
