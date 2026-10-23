package sessioncapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestOpenRejectsUnsafeQueuePathsAndRecovers(t *testing.T) {
	for _, mode := range []string{"parent-file", "wide-queue", "wide-payloads", "symlink-queue", "symlink-payloads", "symlink-database", "wide-database", "unopenable-lock-path", "corrupt-database", "cancelled-init"} {
		t.Run(mode, func(t *testing.T) {
			dir, opts, _ := setup(t)
			ctx := t.Context()
			if mode == "parent-file" {
				write(t, dir, "not a directory")
			} else if err := os.MkdirAll(filepath.Join(dir, "payloads"), 0o700); err != nil {
				t.Fatal(err)
			}
			var repair func()
			switch mode {
			case "parent-file":
				repair = func() { removeFailureFixture(t, dir) }
			case "wide-queue", "wide-payloads":
				path := dir
				if mode == "wide-payloads" {
					path = filepath.Join(dir, "payloads")
				}
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
				repair = func() {
					if err := os.Chmod(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "symlink-queue", "symlink-payloads", "symlink-database":
				path := dir
				target := filepath.Join(t.TempDir(), "target")
				if mode == "symlink-payloads" {
					path = filepath.Join(dir, "payloads")
				}
				if mode == "symlink-database" {
					path = filepath.Join(dir, "queue.sqlite")
					write(t, target, "untouched target")
				} else {
					removeFailureFixture(t, path)
					if err := os.MkdirAll(filepath.Join(target, "payloads"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				repair = func() {
					if mode == "symlink-database" {
						got, err := os.ReadFile(target)
						if err != nil || string(got) != "untouched target" {
							t.Fatalf("unsafe database target changed: %q, %v", got, err)
						}
					}
					removeFailureFixture(t, path)
				}
			case "wide-database":
				path := filepath.Join(dir, "queue.sqlite")
				write(t, path, "")
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				repair = func() {
					if err := os.Chmod(path, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "unopenable-lock-path":
				path := filepath.Join(dir, "queue.lock")
				// flock can lock directories on Darwin. A dangling target below a
				// missing parent fails opening the lock on both supported platforms.
				if err := os.Symlink(filepath.Join(dir, "missing-parent", "lock"), path); err != nil {
					t.Fatal(err)
				}
				repair = func() { removeFailureFixture(t, path) }
			case "corrupt-database":
				path := filepath.Join(dir, "queue.sqlite")
				write(t, path, "not a SQLite database")
				repair = func() {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != "not a SQLite database" {
						t.Fatalf("failed initialization changed evidence: %q, %v", got, err)
					}
					removeFailureFixture(t, path)
				}
			case "cancelled-init":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				repair = func() {}
			}
			q, err := Open(ctx, dir, opts)
			if q != nil {
				if closeErr := q.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("unsafe or failed initialization returned a queue")
			}
			if err == nil {
				t.Fatal("expected open failure")
			}
			if mode == "cancelled-init" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost initialization cancellation: %v", err)
			}
			repair()
			// The same path must reopen: in particular initialization failures must
			// close SQLite and release the process lock, not strand an owner.
			q = open(t, dir, opts)
			status, err := q.Status(t.Context())
			if err != nil || len(status) != 0 {
				t.Fatalf("failed open invented jobs: %+v, %v", status, err)
			}
		})
	}
}

func removeFailureFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenArchiveMismatchPreservesAcceptedQueue(t *testing.T) {
	dir, opts, req := setup(t)
	q, err := Open(t.Context(), dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	accepted := capture(t, q, req)
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := opts
	wrong.ArchiveID = "different-archive"
	if other, err := Open(t.Context(), dir, wrong); err == nil || other != nil {
		if other != nil {
			if err := other.Close(); err != nil {
				t.Error(err)
			}
		}
		t.Fatal("archive mismatch accepted")
	}
	q = open(t, dir, opts)
	status, err := q.Status(t.Context())
	if err != nil || len(status) != 1 || status[0].PublicationID != accepted.PublicationID || status[0].State != "pending" {
		t.Fatalf("mismatched open damaged pending work: %+v, %v", status, err)
	}
	if err := q.verify(t.Context(), accepted.Sources[0].Object); err != nil {
		t.Fatalf("mismatched open damaged accepted bytes: %v", err)
	}
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, req.Identity); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureSourceFailuresPreserveProgressAndRetry(t *testing.T) {
	for _, mode := range []string{"directory", "oversize-sparse", "spool-directory", "missing-spool", "missing-original", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			dir, opts, req := setup(t)
			q := open(t, dir, opts)
			before, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			var want error
			var repair func()
			switch mode {
			case "directory":
				removeFailureFixture(t, req.Path)
				if err := os.Mkdir(req.Path, 0o700); err != nil {
					t.Fatal(err)
				}
				repair = func() { removeFailureFixture(t, req.Path) }
			case "oversize-sparse":
				// Truncate creates a sparse fixture; no gigabyte is allocated or read.
				if err := os.Truncate(req.Path, MaxSourceBytes+1); err != nil {
					t.Fatal(err)
				}
				want = ErrQuota
				repair = func() {
					if err := os.Truncate(req.Path, 0); err != nil {
						t.Fatal(err)
					}
				}
			case "spool-directory":
				path := filepath.Join(dir, "payloads", "unexpected-directory")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				want = ErrIntegrity
				repair = func() { removeFailureFixture(t, path) }
			case "missing-spool":
				path := filepath.Join(dir, "payloads")
				removeFailureFixture(t, path)
				want = os.ErrNotExist
				repair = func() {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "missing-original":
				removeFailureFixture(t, req.Path)
				want = os.ErrNotExist
				repair = func() {}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				want = context.Canceled
				repair = func() {}
			}
			if _, err := q.Capture(ctx, req); err == nil || (want != nil && !errors.Is(err, want)) {
				t.Fatalf("want failure %v, got %v", want, err)
			} else if mode == "directory" && !strings.Contains(err.Error(), "regular source required") {
				t.Fatalf("directory rejected at wrong boundary: %v", err)
			}
			after, err := q.Status(t.Context())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed capture advanced progress: %+v -> %+v, %v", before, after, err)
			}
			repair()
			entries, err := os.ReadDir(filepath.Join(dir, "payloads"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed snapshot left staged payload: %v, %v", entries, err)
			}
			write(t, req.Path, "healthy retry\n")
			m := capture(t, q, req)
			if m.Sequence != 1 || len(m.Sources) != 1 || m.Sources[0].Revision != 1 || m.Sources[0].Object.SHA256 != digest([]byte("healthy retry\n")) {
				t.Fatal("failed capture advanced source ordering or lost retry bytes")
			}
		})
	}
}

func TestMissingOriginalDoesNotEraseAcceptedProgress(t *testing.T) {
	dir, opts, req := setup(t)
	q := open(t, dir, opts)
	accepted := capture(t, q, req)
	before, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	removeFailureFixture(t, req.Path)
	if _, err := q.Capture(t.Context(), req); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing original must fail capture: %v", err)
	}
	after, err := q.Status(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("missing source erased accepted progress: %+v -> %+v, %v", before, after, err)
	}
	if err := q.verify(t.Context(), accepted.Sources[0].Object); err != nil {
		t.Fatalf("missing original damaged accepted payload: %v", err)
	}
	write(t, req.Path, "first\nsecond\n")
	retry := capture(t, q, req)
	if retry.Sequence != 2 || len(retry.Sources) != 1 || retry.Sources[0].EpochID != accepted.Sources[0].EpochID || retry.Sources[0].Revision != 2 {
		t.Fatal("missing original advanced ordering or broke append continuity")
	}
}

func TestSnapshotFailureRemovesTemporaryPayload(t *testing.T) {
	for _, mode := range []string{"cancelled", "missing-original"} {
		t.Run(mode, func(t *testing.T) {
			dir, opts, req := setup(t)
			q := open(t, dir, opts)
			ctx := t.Context()
			want := os.ErrNotExist
			if mode == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				want = context.Canceled
			} else {
				// The original can disappear after policy resolution but before open.
				removeFailureFixture(t, req.Path)
			}
			obj, prefix, err := q.snapshot(ctx, req, req.Path, publication.Source{})
			if !errors.Is(err, want) || obj != (publication.Object{}) || prefix != "" {
				t.Fatalf("failed snapshot returned accepted content: %+v, %q, %v", obj, prefix, err)
			}
			entries, err := os.ReadDir(filepath.Join(dir, "payloads"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed snapshot retained temporary bytes: %v, %v", entries, err)
			}
			write(t, req.Path, "first\n")
			m := capture(t, q, req)
			if m.Sequence != 1 {
				t.Fatal("failed snapshot advanced progress")
			}
		})
	}
}

func TestStableObservationDetectsRestoredMetadataAndReplacement(t *testing.T) {
	for _, mode := range []string{"same-size-rewrite", "path-replacement", "missing-path"} {
		t.Run(mode, func(t *testing.T) {
			_, _, req := setup(t)
			f, err := os.Open(req.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Error(err)
				}
			}()
			before, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "same-size-rewrite":
				write(t, req.Path, "other\n")
				if err := os.Chtimes(req.Path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "path-replacement":
				if err := os.Rename(req.Path, req.Path+".old"); err != nil {
					t.Fatal(err)
				}
				write(t, req.Path, "first\n")
				if err := os.Chtimes(req.Path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "missing-path":
				if err := os.Remove(req.Path); err != nil {
					t.Fatal(err)
				}
			}
			err = stableObservation(t.Context(), f, req.Path, before, 6, digest([]byte("first\n")))
			if err == nil {
				t.Fatal("source mutation accepted as stable")
			}
			if mode == "missing-path" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("lost missing source error: %v", err)
				}
			} else if !strings.Contains(err.Error(), "source changed during observation") {
				t.Fatalf("wrong mutation error: %v", err)
			}
		})
	}
}
