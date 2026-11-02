package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// burstRequests creates n sessions, each with its own one-line source file.
func burstRequests(t *testing.T, base Request, n int) ([]Request, []string) {
	t.Helper()
	rs := make([]Request, n)
	paths := make([]string, n)
	for i := range rs {
		r := base
		r.Identity.SessionID = fmt.Sprintf("s%d", i)
		r.Path = filepath.Join(t.TempDir(), "t.jsonl")
		if err := os.WriteFile(r.Path, []byte("line0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		rs[i], paths[i] = r, r.Path
	}
	return rs, paths
}

// runtimeIdle reports that no source is open and every job is published.
func runtimeIdle(ctx context.Context, q *Queue) (bool, error) {
	var open, jobs, unpublished int
	if err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_sources WHERE NOT(state='captured' AND captured_version=version)`).Scan(&open); err != nil {
		return false, err
	}
	if err := q.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(state!='published'),0) FROM jobs`).Scan(&jobs, &unpublished); err != nil {
		return false, err
	}
	return open == 0 && jobs > 0 && unpublished == 0, nil
}

// driveUntilIdle runs the autonomous runtime while during produces hints, then
// waits for it to settle. No explicit capture or delivery call is made.
func driveUntilIdle(t *testing.T, q *Queue, backend publication.Backend, during func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx, backend, RuntimeOptions{PollInterval: 5 * time.Millisecond}) }()
	during()
	deadline := time.Now().Add(60 * time.Second)
	for {
		idle, err := runtimeIdle(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		if idle {
			time.Sleep(300 * time.Millisecond) // must stay idle
			if idle, err = runtimeIdle(t.Context(), q); err != nil {
				t.Fatal(err)
			} else if idle {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime did not reach idle in 60s (lost wakeup?)")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestHintBurstCoalescesAndKeepsLatestBytes(t *testing.T) {
	const sources, workers, perWorker = 8, 4, 250 // 1000 hints
	dir, o, base := setup(t)
	q := open(t, dir, o)
	backend := newBackend(t)
	rs, paths := burstRequests(t, base, sources)
	final := make([]string, sources)
	var locks [sources]sync.Mutex
	for i := range final {
		final[i] = "line0\n"
	}
	appendLine := func(i int, text string) {
		locks[i].Lock()
		defer locks[i].Unlock()
		final[i] += text
		f, err := os.OpenFile(paths[i], os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Error(err)
		}
	}
	driveUntilIdle(t, q, backend, func() {
		var wg sync.WaitGroup
		for g := range workers {
			wg.Go(func() {
				for k := range perWorker {
					i := (g + k) % sources
					r := rs[i]
					r.ObservedAtNS = int64(g*perWorker + k + 1)
					if k%50 == 0 {
						appendLine(i, fmt.Sprintf("g%d-k%d\n", g, k))
					}
					if err := q.Hint(t.Context(), r); err != nil {
						t.Errorf("hint: %v", err)
						return
					}
				}
			})
		}
		wg.Wait()
		for i := range sources { // latest bytes, then the final wakeup
			appendLine(i, "FINAL\n")
			r := rs[i]
			r.ObservedAtNS = 1 << 40
			if err := q.Hint(t.Context(), r); err != nil {
				t.Fatal(err)
			}
		}
	})
	var rows, maxAttempts int
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*),COALESCE(MAX(attempts),0) FROM runtime_sources`).Scan(&rows, &maxAttempts); err != nil {
		t.Fatal(err)
	}
	if rows != sources || maxAttempts != 0 {
		t.Fatalf("runtime_sources rows=%d (want %d) max attempts=%d (want 0)", rows, sources, maxAttempts)
	}
	for i := range sources {
		dest := filepath.Join(t.TempDir(), "x")
		exp, err := publication.ExportSession(t.Context(), backend, rs[i].Identity, dest)
		if err != nil || len(exp.Bodies) != 1 {
			t.Fatalf("source %d export: %v bodies=%d", i, err, len(exp.Bodies))
		}
		raw, err := os.ReadFile(filepath.Join(dest, exp.Bodies[0].Path))
		locks[i].Lock()
		want := final[i]
		locks[i].Unlock()
		if err != nil || string(raw) != want {
			t.Fatalf("source %d latest bytes lost: got %d bytes want %d (%v)", i, len(raw), len(want), err)
		}
	}
}

func TestHintInboxIsBounded(t *testing.T) {
	dir, o, base := setup(t)
	q := open(t, dir, o)
	total := MaxInboxHints + 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted, refused := 0, 0
	for g := range 8 {
		wg.Go(func() {
			for k := g; k < total*2; k += 8 { // each source id hinted twice
				r := base
				r.SourceID = fmt.Sprintf("src%d", k%total)
				err := WriteHint(t.Context(), dir, r)
				mu.Lock()
				switch {
				case err == nil:
					accepted++
				case errors.Is(err, ErrQuota):
					refused++
				default:
					t.Errorf("WriteHint: %v", err)
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	files, err := os.ReadDir(filepath.Join(dir, "hints"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) > MaxInboxHints {
		t.Fatalf("%d inbox files exceed MaxInboxHints %d", len(files), MaxInboxHints)
	}
	if accepted == 0 || refused == 0 {
		t.Fatalf("bound not exercised: accepted=%d refused=%d", accepted, refused)
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := q.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_sources`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadDir(filepath.Join(dir, "hints"))
	if err != nil {
		t.Fatal(err)
	}
	if rows > MaxInboxHints || len(left) != 0 {
		t.Fatalf("rows=%d (max %d) leftover inbox files=%d", rows, MaxInboxHints, len(left))
	}
}
