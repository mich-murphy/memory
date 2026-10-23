package sessioncapture

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestQuotaAuditInstallCancellationReleasesTemporary(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	first := capture(t, q, r)
	obj := first.Sources[0].Object
	lock := keyedMutex(&q.installs, obj.EncodedSHA256)
	lock.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	q.opts.Fault = func(stage string) error {
		if stage == "payload-before-sync" {
			cancel() // All encoded bytes are reserved before the install wait.
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := q.Capture(ctx, r); done <- err }()
	select {
	case err := <-done:
		lock.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("install wait: %v", err)
		}
	case <-time.After(time.Second):
		lock.Unlock()
		<-done
		t.Fatal("canceled capture waited for identical payload install")
	}
	used, err := q.usage()
	if err != nil {
		t.Fatal(err)
	}
	if used != obj.EncodedSize || q.used != used || q.writing != 0 {
		t.Fatalf("canceled install charge: physical=%d used=%d writing=%d", used, q.used, q.writing)
	}
	statuses, err := q.Status(t.Context())
	if err != nil || len(statuses) != 1 {
		t.Fatalf("canceled install advanced progress: %d %v", len(statuses), err)
	}
}

func TestQuotaAuditConcurrentScansAndTemporaryMoves(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	// Repeated identical captures exercise both rename and duplicate removal.
	// Every scan must succeed regardless of the metadata interleaving.
	start := make(chan struct{})
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for range 100 {
			if _, err := q.Capture(t.Context(), r); err != nil {
				failures <- err
				return
			}
		}
	})
	wg.Go(func() {
		<-start
		for range 1000 {
			if _, err := q.usage(); err != nil {
				failures <- err
				return
			}
		}
	})
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	used, err := q.usage()
	if err != nil || used != q.used || q.writing != 0 {
		t.Fatalf("post-race usage: %d %d %d %v", used, q.used, q.writing, err)
	}
}

func TestQuotaAuditUnsafePermissionsBlockNoopAndCapture(t *testing.T) {
	for _, target := range []string{"payload", "directory", "foreign-file"} {
		t.Run(target, func(t *testing.T) {
			dir, o, r := setup(t)
			q := open(t, dir, o)
			first := capture(t, q, r)
			q.opts.MaxSpoolBytes = q.used
			path := q.payload(first.Sources[0].Object)
			mode := os.FileMode(0o640)
			switch target {
			case "directory":
				path = filepath.Join(dir, "payloads")
				mode = 0o750
			case "foreign-file":
				path = filepath.Join(dir, "payloads", "external")
				write(t, path, "synthetic orphan")
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := q.CaptureIfChanged(t.Context(), r); err == nil {
				t.Fatal("full-quota no-op ignored unsafe spool permissions")
			}
			if _, err := q.Capture(t.Context(), r); err == nil || errors.Is(err, ErrQuota) {
				t.Fatalf("capture ignored unsafe spool permissions: %v", err)
			}
		})
	}
}

func TestQuotaAuditArithmetic(t *testing.T) {
	for _, test := range []struct {
		blocks uint64
		size   int64
		want   int64
		bad    bool
	}{
		{0, 4096, 0, false},
		{3, 4096, 12288, false},
		{math.MaxUint64, 4096, math.MaxInt64, false},
		{math.MaxInt64, 1, math.MaxInt64, false},
		{1, 0, 0, true},
		{1, -1, 0, true},
	} {
		got, err := filesystemAvailable(test.blocks, test.size)
		if got != test.want || (err != nil) != test.bad {
			t.Fatalf("filesystemAvailable(%d,%d)=%d,%v", test.blocks, test.size, got, err)
		}
	}
	q := &Queue{opts: Options{MaxSpoolBytes: math.MaxInt64}, used: math.MaxInt64 - 1}
	if err := q.reserve(-1); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("negative reservation: %v", err)
	}
	if err := q.reserve(2); !errors.Is(err, ErrQuota) {
		t.Fatalf("overflow reservation: %v", err)
	}
	if err := q.reserve(1); err != nil || q.used != math.MaxInt64 || q.writing != 1 {
		t.Fatalf("exact max reservation: %d %d %v", q.used, q.writing, err)
	}
	if err := q.reserve(1); !errors.Is(err, ErrQuota) {
		t.Fatalf("max exceeded: %v", err)
	}
}

func TestQuotaAuditNoopHistoricalPolicyAndLearnedAlias(t *testing.T) {
	dir, o, r := setup(t)
	original := r.Path
	alias := filepath.Join(filepath.Dir(original), "alias.jsonl")
	other := filepath.Join(filepath.Dir(original), "other.jsonl")
	write(t, other, "first\n")
	if err := os.Symlink(original, alias); err != nil {
		t.Fatal(err)
	}
	r.Path = alias
	denied := ""
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if req.Path == denied {
			return errors.New("denied")
		}
		return nil
	}
	q := open(t, dir, o)
	first := capture(t, q, r)
	// Use exact owned bytes, irrespective of the setup fixture's contents.
	body, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, alias); err != nil {
		t.Fatal(err)
	}
	second, err := q.CaptureIfChanged(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != first.Sequence+1 || second.Sources[0].EpochID != first.Sources[0].EpochID {
		t.Fatal("new alias evidence was skipped or changed the source epoch")
	}
	_, ev, err := q.latest(t.Context(), r.Identity)
	if err != nil || len(ev) != 1 || !slices.Contains(ev[0].Identities, original) || !slices.Contains(ev[0].Identities, other) {
		t.Fatalf("historical policy identities lost: %v", err)
	}
	q.opts.MaxSpoolBytes = q.used
	denied = original
	if _, err := q.CaptureIfChanged(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatalf("no-op bypassed historical policy: %v", err)
	}
}

// A stale directory entry must remain an error: internal metadata mutations are
// fenced by payloadMu, so an ENOENT here is not an internal temporary race.
func TestQuotaAuditMissingEntriesFailClosed(t *testing.T) {
	for _, name := range []string{"capture-external-orphan", "retained.zst"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			write(t, path, "synthetic bytes")
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err := payloadUsage(entries); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing external entry silently skipped: %v", err)
			}
		})
	}
}

type quotaAuditSizedInfo struct {
	os.FileInfo
	size int64
}

func (s quotaAuditSizedInfo) Size() int64 { return s.size }

type quotaAuditSizedEntry struct {
	os.DirEntry
	info os.FileInfo
}

func (e quotaAuditSizedEntry) Info() (os.FileInfo, error) { return e.info, nil }

func TestQuotaAuditUsageSizeOverflow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload")
	write(t, path, "synthetic bytes")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, sizes := range [][]int64{{-1}, {math.MaxInt64, 1}, {math.MaxInt64 - 1, 2}} {
		entries := make([]os.DirEntry, 0, len(sizes))
		for _, size := range sizes {
			entries = append(entries, quotaAuditSizedEntry{info: quotaAuditSizedInfo{FileInfo: info, size: size}})
		}
		if _, err := payloadUsage(entries); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("invalid usage sizes %v: %v", sizes, err)
		}
	}
}

func TestQuotaAuditRetainedChargeAfterDurableBoundaryFault(t *testing.T) {
	for _, stage := range []string{"payload-synced", "job-before-commit"} {
		for _, duplicate := range []bool{false, true} {
			name := stage + "/new"
			if duplicate {
				name = stage + "/duplicate"
			}
			t.Run(name, func(t *testing.T) {
				dir, o, r := setup(t)
				q := open(t, dir, o)
				if duplicate {
					capture(t, q, r)
				}
				injected := errors.New("injected durable boundary fault")
				q.opts.Fault = func(at string) error {
					// Fault callbacks must not run under the quota mutex.
					if err := q.reserve(0); err != nil {
						return err
					}
					if at == stage {
						return injected
					}
					return nil
				}
				if _, err := q.Capture(t.Context(), r); !errors.Is(err, injected) {
					t.Fatalf("fault capture: %v", err)
				}
				used, err := q.usage()
				if err != nil || used == 0 || q.used != used || q.writing != 0 {
					t.Fatalf("durable payload charge: physical=%d used=%d writing=%d err=%v", used, q.used, q.writing, err)
				}
				entries, err := os.ReadDir(filepath.Join(dir, "payloads"))
				if err != nil || len(entries) != 1 {
					t.Fatalf("retained payload missing or temporary leaked: %d %v", len(entries), err)
				}
				statuses, err := q.Status(t.Context())
				want := 0
				if duplicate {
					want = 1
				}
				if err != nil || len(statuses) != want {
					t.Fatalf("fault advanced progress: %d %v", len(statuses), err)
				}
			})
		}
	}
}
