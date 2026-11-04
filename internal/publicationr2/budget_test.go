package publicationr2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBodyBudgetTotalExactTable(t *testing.T) {
	d := DefaultBodyBudget()
	for _, tc := range []struct {
		size int64
		want time.Duration
	}{
		{-5, time.Minute},
		{0, time.Minute},
		{30 << 20, time.Minute},
		{31 << 20, 61 * time.Second},
		{100 << 20, 130 * time.Second},
		{MaxObjectSize, 15 * time.Minute},
		{MaxObjectSize * 2, 15 * time.Minute},
	} {
		if got := d.Total(tc.size); got != tc.want {
			t.Fatalf("Total(%d)=%s want %s", tc.size, got, tc.want)
		}
	}
	slow := d
	slow.MinThroughput = 1 << 10
	if got := slow.Total(100 << 10); got != 130*time.Second {
		t.Fatalf("slow Total=%s", got)
	}
}

func TestBodyBudgetValidation(t *testing.T) {
	good := DefaultBodyBudget()
	for name, change := range map[string]func(*BodyBudget){
		"base":       func(b *BodyBudget) { b.Base = -1 },
		"throughput": func(b *BodyBudget) { b.MinThroughput = 0 },
		"floor":      func(b *BodyBudget) { b.Floor = 0 },
		"cap":        func(b *BodyBudget) { b.Cap = b.Floor - 1 },
		"idle":       func(b *BodyBudget) { b.Idle = 0 },
	} {
		b := good
		change(&b)
		if _, err := New(clientFor(t, http.NotFoundHandler()), "offline-bucket", time.Second, WithBodyBudget(b)); err == nil {
			t.Fatalf("%s: invalid budget accepted", name)
		}
	}
	if _, err := New(clientFor(t, http.NotFoundHandler()), "offline-bucket", time.Second, WithBodyBudget(good)); err != nil {
		t.Fatal(err)
	}
}

func budgetBackend(t *testing.T, h http.Handler, flat time.Duration, budget BodyBudget) *Backend {
	t.Helper()
	b, err := New(clientFor(t, h), "offline-bucket", flat, WithBodyBudget(budget))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writePayload(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestObjectUploadOutlivesFlatTimeout(t *testing.T) {
	p := newProtocol()
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			time.Sleep(500 * time.Millisecond)
		}
		p.ServeHTTP(w, r)
	})
	budget := BodyBudget{Base: 5 * time.Second, MinThroughput: 1 << 20, Floor: 5 * time.Second, Cap: 5 * time.Second, Idle: 2 * time.Second}
	b := budgetBackend(t, slow, 150*time.Millisecond, budget)
	data := []byte("object payload longer than the flat budget")
	obj, manifest := payloadKeys(data)
	if err := b.PutFile(t.Context(), obj, writePayload(t, data)); err != nil {
		t.Fatalf("object upload under body budget: %v", err)
	}
	// Manifests are metadata and keep the flat deadline.
	if err := b.PutFile(t.Context(), manifest, writePayload(t, data)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("manifest upload: %v", err)
	}
}

func TestObjectUploadTotalBudgetIsEnforced(t *testing.T) {
	// Drain the body first: net/http only notices a client disconnect after it is read.
	block := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() })
	budget := BodyBudget{MinThroughput: 1 << 20, Floor: 200 * time.Millisecond, Cap: 200 * time.Millisecond, Idle: time.Minute}
	b := budgetBackend(t, block, time.Minute, budget)
	data := []byte("object")
	obj, _ := payloadKeys(data)
	start := time.Now()
	err := b.PutFile(t.Context(), obj, writePayload(t, data))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestStalledObjectBodyTimesOutAsRetryable(t *testing.T) {
	stall := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("0123456789"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	budget := BodyBudget{Base: time.Minute, MinThroughput: 1 << 20, Floor: time.Minute, Cap: time.Minute, Idle: 200 * time.Millisecond}
	b := budgetBackend(t, stall, time.Minute, budget)
	obj, _ := payloadKeys()
	r, err := b.Open(t.Context(), obj)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = io.ReadAll(r)
	elapsed := time.Since(start)
	if !errors.Is(err, errStalled) || !errors.Is(err, context.DeadlineExceeded) || elapsed > 5*time.Second {
		t.Fatalf("err=%v after %s", err, elapsed)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSlowButProgressingObjectBodyIsNotStalled(t *testing.T) {
	body := []byte("0123456789")
	trickle := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusOK)
		for i := range body {
			_, _ = w.Write(body[i : i+1])
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	// Total transfer (about 1s) exceeds Idle (400ms), but each chunk arrives in time.
	budget := BodyBudget{Base: time.Minute, MinThroughput: 1 << 20, Floor: time.Minute, Cap: time.Minute, Idle: 400 * time.Millisecond}
	b := budgetBackend(t, trickle, time.Minute, budget)
	obj, _ := payloadKeys()
	r, err := b.Open(t.Context(), obj)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("got %q err=%v", got, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTransferBudgetIsTwiceTotal(t *testing.T) {
	b := budgetBackend(t, http.NotFoundHandler(), time.Second, DefaultBodyBudget())
	for _, tc := range []struct {
		size int64
		want time.Duration
	}{
		{0, 2 * time.Minute},
		{100 << 20, 260 * time.Second},
		{MaxObjectSize, 30 * time.Minute},
	} {
		if got := b.TransferBudget(tc.size); got != tc.want || got != 2*b.body.Total(tc.size) {
			t.Fatalf("TransferBudget(%d)=%s want %s", tc.size, got, tc.want)
		}
	}
}

// After the request body is fully sent only the destination's response remains, so
// the idle guard must be disarmed: a slow response within Total is not a stall.
func TestIdleGuardDisarmedWhileAwaitingResponse(t *testing.T) {
	p := newProtocol()
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			time.Sleep(700 * time.Millisecond)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		p.ServeHTTP(w, r)
	})
	budget := BodyBudget{Base: time.Minute, MinThroughput: 1 << 20, Floor: time.Minute, Cap: time.Minute, Idle: 250 * time.Millisecond}
	b := budgetBackend(t, slow, time.Minute, budget)
	data := []byte("payload awaiting a slow response")
	obj, _ := payloadKeys(data)
	if err := b.PutFile(t.Context(), obj, writePayload(t, data)); err != nil {
		t.Fatalf("slow response treated as stall: %v", err)
	}
}
