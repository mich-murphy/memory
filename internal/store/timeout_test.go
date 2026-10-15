package store_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/store"
)

// hungR2 is an R2 client whose server never answers, or, with partialBody, sends headers and
// one byte of a longer body, then stops.
func hungR2(t *testing.T, partialBody bool) *store.R2 {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if partialBody {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs first, so Close isn't held by a hung handler
	r2, err := store.NewR2(&config.Config{
		R2Endpoint: srv.URL, R2Bucket: "sessions", R2AccessKeyID: "id", R2SecretAccessKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r2
}

func TestEveryR2RequestEndsAtItsDeadline(t *testing.T) {
	store.SetOpTimeout(t, 200*time.Millisecond)
	store.SetMultipart(t, 1<<20, 5<<20)
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := map[string]func(context.Context, *store.R2) error{
		"put":       func(ctx context.Context, r *store.R2) error { return r.Put(ctx, "k", []byte("b"), nil) },
		"multipart": func(ctx context.Context, r *store.R2) error { return r.PutFile(ctx, "k", big, nil) },
		"create":    func(ctx context.Context, r *store.R2) error { return r.Create(ctx, "k", []byte("b")) },
		"head": func(ctx context.Context, r *store.R2) error {
			_, err := r.Head(ctx, "k")
			return err
		},
		"list": func(ctx context.Context, r *store.R2) error {
			_, err := r.List(ctx, "", "", 10)
			return err
		},
		"get": func(ctx context.Context, r *store.R2) error {
			_, err := r.Get(ctx, "k")
			return err
		},
		"copy": func(ctx context.Context, r *store.R2) error {
			_, err := r.Copy(ctx, "a", "b", "sha")
			return err
		},
		"delete": func(ctx context.Context, r *store.R2) error { return r.Delete(ctx, "k") },
		"open": func(ctx context.Context, r *store.R2) error {
			rc, err := r.OpenReader(ctx, "k")
			if err != nil {
				return err
			}
			return rc.Close()
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			err := withinSeconds(t, 10, func() error { return op(t.Context(), hungR2(t, false)) })
			if err == nil {
				t.Errorf("%s against a hung server = nil, want an error", name)
			}
		})
	}
}

// The body is read after OpenReader returns, so its deadline must outlive the call.
func TestR2OpenReaderBodyReadEndsAtItsDeadline(t *testing.T) {
	store.SetOpTimeout(t, 200*time.Millisecond)
	rc, err := hungR2(t, true).OpenReader(t.Context(), "k")
	if err != nil {
		t.Fatalf("OpenReader = %v, want a reader", err)
	}
	defer func() { _ = rc.Close() }() // read-only
	err = withinSeconds(t, 10, func() error {
		_, err := io.ReadAll(rc)
		return err
	})
	if err == nil {
		t.Error("ReadAll of a stalled body = nil, want an error")
	}
}

func withinSeconds(t *testing.T, seconds int, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Duration(seconds) * time.Second):
		t.Fatalf("still running after %ds; the request has no deadline", seconds)
		return nil
	}
}
