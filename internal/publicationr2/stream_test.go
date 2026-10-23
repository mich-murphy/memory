package publicationr2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestConcurrentReplacement(t *testing.T) {
	b := backendFor(t, newProtocol(), 5*time.Second)
	key := identity.CurrentKey()
	if err := b.CompareAndSwap(t.Context(), key, "", []byte("old")); err != nil {
		t.Fatal(err)
	}
	_, token, err := b.Current(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, body := range []string{"next-a", "next-b"} {
		go func() { results <- b.CompareAndSwap(t.Context(), key, token, []byte(body)) }()
	}
	first, second := <-results, <-results
	if (first != nil || !errors.Is(second, publication.ErrConflict)) && (second != nil || !errors.Is(first, publication.ErrConflict)) {
		t.Fatalf("replacement results: %v %v", first, second)
	}
	body, _, err := b.Current(t.Context(), key)
	if err != nil || string(body) != "next-a" && string(body) != "next-b" {
		t.Fatalf("current replacement: %q %v", body, err)
	}
}

func TestOpenLengthValidation(t *testing.T) {
	obj, _ := payloadKeys()
	for _, tc := range []struct {
		name, key, length string
		openError         bool
	}{
		{"metadata-too-large", identity.CurrentKey(), strconv.Itoa(publication.MaxMetadata + 1), true},
		{"object-too-large", obj, strconv.FormatInt(MaxObjectSize+1, 10), true},
		{"short-body", obj, "100", false},
		{"unknown-length", obj, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.length != "" {
					w.Header().Set("Content-Length", tc.length)
				}
				if tc.length == "" {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				_, _ = w.Write([]byte("short"))
			}), time.Second)
			r, err := b.Open(t.Context(), tc.key)
			if tc.openError {
				if err == nil {
					_ = r.Close()
					t.Fatal("invalid length accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(r)
			closeErr := r.Close()
			if err == nil || closeErr != nil {
				t.Fatalf("short response: %v %v", err, closeErr)
			}
		})
	}
}

func TestCloseCancelsUnreadStream(t *testing.T) {
	done := make(chan struct{})
	b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(done)
	}), 5*time.Second)
	r, err := b.Open(t.Context(), identity.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel request")
	}
}

func TestCurrentDeadlineIncludesBody(t *testing.T) {
	b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Header().Set("ETag", `"version"`)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}), 100*time.Millisecond)
	if _, _, err := b.Current(t.Context(), identity.CurrentKey()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body deadline: %v", err)
	}
}

func TestPutFileDeadline(t *testing.T) {
	b := backendFor(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}), 100*time.Millisecond)
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	obj, _ := payloadKeys()
	if err := b.PutFile(t.Context(), obj, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PUT deadline: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPutFileLengthMismatch(t *testing.T) {
	p := newProtocol()
	b := backendFor(t, p, time.Second)
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 100000), 0o600); err != nil {
		t.Fatal(err)
	}
	// Truncate after the SDK has signed the fixed-length request but before the
	// actual HTTP transport reads it. The endpoint must not accept short data.
	original := b.client.Options().HTTPClient
	options := b.client.Options()
	options.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := os.Truncate(path, 0); err != nil {
			return nil, err
		}
		return original.Do(r)
	})}
	b.client = s3.New(options)
	obj, _ := payloadKeys(bytes.Repeat([]byte("x"), 100000))
	if err := b.PutFile(t.Context(), obj, path); err == nil {
		t.Fatal("shortened source accepted")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.objects[obj]; ok {
		t.Fatal("short source committed")
	}
}

func TestStreamExcessLength(t *testing.T) {
	// A malicious/custom SDK HTTP transport can return more than its declared
	// length; normal net/http already enforces this at the protocol boundary.
	b := backendFor(t, newProtocol(), time.Second)
	options := b.client.Options()
	options.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Length": []string{"1"}, "Etag": []string{`"v"`}}, Body: io.NopCloser(strings.NewReader("too long")), ContentLength: 1, Request: r}, nil
	})}
	b.client = s3.New(options)
	r, err := b.Open(t.Context(), identity.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r)
	if closeErr := r.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err == nil {
		t.Fatal("overlong body accepted")
	}
}

func TestOpaqueETag(t *testing.T) {
	const token = `"opaque-not-a-content-md5-17"`
	var match string
	b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", token)
			w.Header().Set("Content-Length", "2")
			_, _ = w.Write([]byte("{}"))
			return
		}
		match = r.Header.Get("If-Match")
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}), time.Second)
	_, got, err := b.Current(t.Context(), identity.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("token transformed: %q", got)
	}
	if err := b.CompareAndSwap(t.Context(), identity.CurrentKey(), got, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if match != token {
		t.Fatalf("conditional token transformed: %q", match)
	}
}

func TestArchiveCreateOnly(t *testing.T) {
	b := backendFor(t, newProtocol(), time.Second)
	body := []byte(`{"contract":"session-publication/v1","archive_id":"offline"}`)
	if err := b.CompareAndSwap(t.Context(), publication.ArchiveKey, "", body); err != nil {
		t.Fatal(err)
	}
	got, token, err := b.Current(t.Context(), publication.ArchiveKey)
	if err != nil || !bytes.Equal(got, body) || token == "" {
		t.Fatalf("archive: %v", err)
	}
	r, err := b.Open(t.Context(), publication.ArchiveKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.CompareAndSwap(t.Context(), publication.ArchiveKey, "", body); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("archive overwrite: %v", err)
	}
}

func TestTokenValidation(t *testing.T) {
	for _, token := range []string{`""`, `"bad token"`, "\"bad\x7f\"", "\"bad\x00\"", `W/"weak"`, `"` + strings.Repeat("x", 1024) + `"`} {
		if validToken(token) {
			t.Fatalf("invalid token accepted: %q", token)
		}
	}
	// No interpretation of multipart-like or non-digest values.
	if !validToken(`"opaque-17"`) {
		t.Fatal("opaque token rejected")
	}
}
