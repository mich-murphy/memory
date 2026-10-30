package sessioncapture

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func noisyBytes() []byte {
	b := make([]byte, 1<<20)
	rng := rand.New(rand.NewPCG(31, 97))
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

func TestParallelCaptureQuotaAndDedup(t *testing.T) {
	for _, enough := range []bool{false, true} {
		t.Run(map[bool]string{false: "peak-rejected", true: "deduplicated"}[enough], func(t *testing.T) {
			dir, o, a := setup(t)
			data := noisyBytes()
			if err := os.WriteFile(a.Path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			b := a
			b.Identity.SessionID = "other"
			b.Path = filepath.Join(filepath.Dir(a.Path), "other.jsonl")
			if err := os.WriteFile(b.Path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			o.MaxSpoolBytes = int64(len(data)) * 3 / 2
			if enough {
				o.MaxSpoolBytes = int64(len(data)) * 3
			}
			blocked, release := make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			first := true
			o.Fault = func(stage string) error {
				if stage != "payload-before-sync" {
					return nil
				}
				mu.Lock()
				stop := first
				first = false
				mu.Unlock()
				if stop {
					close(blocked)
					<-release
				}
				return nil
			}
			q := open(t, dir, o)
			done := make(chan error, 1)
			go func() { _, err := q.Capture(t.Context(), a); done <- err }()
			<-blocked
			// B must finish while A is suspended in source/payload I/O.
			bdone := make(chan error, 1)
			go func() { _, err := q.Capture(t.Context(), b); bdone <- err }()
			select {
			case err := <-bdone:
				if enough && err != nil {
					t.Error(err)
				}
				if !enough && !errors.Is(err, ErrQuota) {
					t.Errorf("wanted peak quota rejection: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("B blocked behind A")
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			used, err := q.usage()
			if err != nil {
				t.Fatal(err)
			}
			q.mu.Lock()
			charged := q.used
			q.mu.Unlock()
			if used != charged || used > o.MaxSpoolBytes {
				t.Fatalf("physical=%d accounted=%d quota=%d", used, charged, o.MaxSpoolBytes)
			}
			entries, err := os.ReadDir(filepath.Join(dir, "payloads"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("expected one shared payload, got %d", len(entries))
			}
			if enough {
				ma, _, err := q.latest(t.Context(), a.Identity)
				if err != nil {
					t.Fatal(err)
				}
				mb, _, err := q.latest(t.Context(), b.Identity)
				if err != nil {
					t.Fatal(err)
				}
				if ma.Sources[0].Object.EncodedSHA256 != mb.Sources[0].Object.EncodedSHA256 {
					t.Fatal("not deduplicated")
				}
			}
		})
	}
}

func TestCaptureIfChangedAtQuotaAndRestoredMtime(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	first := capture(t, q, r)
	used, err := q.usage()
	if err != nil {
		t.Fatal(err)
	}
	q.opts.MaxSpoolBytes = used
	same, err := q.CaptureIfChanged(t.Context(), r)
	if err != nil || same.PublicationID != first.PublicationID {
		t.Fatalf("no-op at quota: %+v %v", same, err)
	}
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrQuota) {
		t.Fatalf("Capture changed its legacy behavior: %v", err)
	}
	q.opts.MaxSpoolBytes = o.MaxSpoolBytes
	st, err := os.Stat(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.Path, "other\n")
	if err := os.Chtimes(r.Path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := q.CaptureIfChanged(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Sequence != 2 || len(changed.Sources) != 2 {
		t.Fatal("same stat rewrite skipped or epoch lost")
	}
	r.Associations = []string{"new-association"}
	associated, err := q.CaptureIfChanged(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if associated.Sequence != 3 || associated.Sources[1].Revision != 2 {
		t.Fatal("association change skipped")
	}
	// Every serialized observation sees the latest whole manifest, rather than a
	// stale source-only view, and unchanged polls never increment the revision.
	var wg sync.WaitGroup
	results := make(chan publication.Manifest, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() { m, e := q.CaptureIfChanged(t.Context(), r); results <- m; failures <- e })
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Error(e)
		}
	}
	for m := range results {
		if m.PublicationID != associated.PublicationID {
			t.Error("concurrent no-op advanced ordering")
		}
	}
}

func TestSameSessionCaptureOrdering(t *testing.T) {
	dir, o, a := setup(t)
	b := a
	b.SourceID = "second"
	b.Path = filepath.Join(filepath.Dir(a.Path), "second.jsonl")
	write(t, b.Path, "second source\n")
	blocked, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	first := true
	o.Fault = func(stage string) error {
		if stage != "payload-before-sync" {
			return nil
		}
		mu.Lock()
		stop := first
		first = false
		mu.Unlock()
		if stop {
			close(blocked)
			<-release
		}
		return nil
	}
	q := open(t, dir, o)
	type result struct {
		m   publication.Manifest
		err error
	}
	adone, bdone := make(chan result, 1), make(chan result, 1)
	go func() { m, e := q.Capture(t.Context(), a); adone <- result{m, e} }()
	<-blocked
	started := make(chan struct{})
	go func() { close(started); m, e := q.Capture(t.Context(), b); bdone <- result{m, e} }()
	<-started
	close(release)
	ar, br := <-adone, <-bdone
	if ar.err != nil || br.err != nil {
		t.Fatalf("captures: %v %v", ar.err, br.err)
	}
	if ar.m.Sequence != 1 || br.m.Sequence != 2 || len(br.m.Sources) != 2 {
		t.Fatal("same-session publication lost ordered whole manifest")
	}
}

func TestCaptureSessionWaitHonorsCancellation(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	lock := q.session(r.Identity)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := q.Capture(ctx, r); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("session wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled capture waited behind session I/O")
	}
}
