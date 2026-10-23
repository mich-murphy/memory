package publicationr2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/mich-murphy/memory/internal/publication"
)

var identity = publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: "session"}

func clientFor(t *testing.T, handler http.Handler) *s3.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return s3.New(s3.Options{
		Region: "auto", BaseEndpoint: aws.String(server.URL), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("offline-key", "offline-secret", ""),
		HTTPClient:  server.Client(), RetryMaxAttempts: 1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
}

func backendFor(t *testing.T, handler http.Handler, timeout time.Duration) *Backend {
	t.Helper()
	b, err := New(clientFor(t, handler), "offline-bucket", timeout)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type stored struct {
	body []byte
	etag string
}
type request struct {
	method, key, match, none string
	length                   int64
}
type s3Protocol struct {
	mu       sync.Mutex
	objects  map[string]stored
	requests []request
}

func newProtocol() *s3Protocol { return &s3Protocol{objects: map[string]stored{}} }
func (p *s3Protocol) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/offline-bucket/")
	p.requests = append(p.requests, request{r.Method, key, r.Header.Get("If-Match"), r.Header.Get("If-None-Match"), r.ContentLength})
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		protocolError(w, 403, "AccessDenied")
		return
	}
	switch r.Method {
	case http.MethodGet:
		v, ok := p.objects[key]
		if !ok {
			protocolError(w, 404, "NoSuchKey")
			return
		}
		w.Header().Set("ETag", v.etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(v.body)))
		_, _ = w.Write(v.body) // HTTP test peer; disconnects are intentionally permitted.
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil || int64(len(body)) != r.ContentLength {
			protocolError(w, 400, "IncompleteBody")
			return
		}
		old, exists := p.objects[key]
		if r.Header.Get("If-None-Match") == "*" && exists || r.Header.Get("If-Match") != "" && (!exists || old.etag != r.Header.Get("If-Match")) {
			protocolError(w, 412, "PreconditionFailed")
			return
		}
		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		p.objects[key] = stored{body, etag}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
	default:
		protocolError(w, 405, "MethodNotAllowed")
	}
}

func protocolError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>private-response-must-not-leak</Message></Error>", code)
}

func payloadKeys(data ...[]byte) (string, string) {
	body := []byte("payload")
	if len(data) != 0 {
		body = data[0]
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	return identity.BaseKey() + "/objects/" + digest + ".zst", identity.BaseKey() + "/manifests/" + digest + ".json"
}

func TestNativeConditionalProtocol(t *testing.T) {
	p := newProtocol()
	b := backendFor(t, p, time.Second*5)
	ctx := t.Context()
	key := identity.CurrentKey()
	if _, _, err := b.Current(ctx, key); !errors.Is(err, publication.ErrNotFound) {
		t.Fatalf("missing current: %v", err)
	}
	if err := b.CompareAndSwap(ctx, key, "", []byte("one")); err != nil {
		t.Fatal(err)
	}
	body, token, err := b.Current(ctx, key)
	if err != nil || string(body) != "one" || !validToken(token) {
		t.Fatalf("current: %q %q %v", body, token, err)
	}
	if err := b.CompareAndSwap(ctx, key, "", []byte("duplicate")); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("create: %v", err)
	}
	if err := b.CompareAndSwap(ctx, key, token, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := b.CompareAndSwap(ctx, key, token, []byte("stale")); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	got, _, err := b.Current(ctx, key)
	if err != nil || string(got) != "two" {
		t.Fatalf("failed update changed prior success: %q %v", got, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var creates, replaces int
	for _, req := range p.requests {
		if req.method == http.MethodPut {
			switch {
			case req.none == "*" && req.match == "":
				creates++
			case req.match == token && req.none == "":
				replaces++
			default:
				t.Fatalf("not native conditional PUT: %+v", req)
			}
		} else if req.method != http.MethodGet {
			t.Fatalf("unexpected method: %s", req.method)
		}
	}
	if creates != 2 || replaces != 2 {
		t.Fatalf("conditional requests: creates %d replaces %d", creates, replaces)
	}
}

func TestConcurrentFirstCreate(t *testing.T) {
	b := backendFor(t, newProtocol(), 5*time.Second)
	const workers = 12
	results := make(chan error, workers)
	start := make(chan struct{})
	for n := range workers {
		go func() {
			<-start
			results <- b.CompareAndSwap(t.Context(), identity.CurrentKey(), "", []byte(strconv.Itoa(n)))
		}()
	}
	close(start)
	var success, conflict int
	for range workers {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, publication.ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != workers-1 {
		t.Fatalf("success %d conflict %d", success, conflict)
	}
}

func TestPutFileAndOpen(t *testing.T) {
	p := newProtocol()
	b := backendFor(t, p, 5*time.Second)
	data := bytes.Repeat([]byte("offline fixture\n"), 10000)
	obj, manifest := payloadKeys(data)
	for _, key := range []string{obj, manifest} {
		path := filepath.Join(t.TempDir(), "payload")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := b.PutFile(t.Context(), key, path); err != nil {
			t.Fatal(err)
		}
		r, err := b.Open(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil || closeErr != nil || !bytes.Equal(got, data) {
			t.Fatalf("roundtrip: %v %v", err, closeErr)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.requests {
		if r.method == http.MethodPut && (r.length <= 0 || r.match != "" || r.none != "*") {
			t.Fatalf("unexpected file PUT %+v", r)
		}
	}
}

func TestErrorMappingAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		want   error
	}{
		{404, "NoSuchKey", publication.ErrNotFound},
		{412, "PreconditionFailed", publication.ErrConflict},
		{409, "ConditionalRequestConflict", publication.ErrConflict},
		{403, "AccessDenied", ErrDestinationPaused},
		{401, "Unauthorized", ErrDestinationPaused},
		{500, "InternalError", nil},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { protocolError(w, tc.status, tc.code) }), time.Second)
			operations := []func() error{
				func() error { _, err := b.Open(t.Context(), identity.CurrentKey()); return err },
				func() error { _, _, err := b.Current(t.Context(), identity.CurrentKey()); return err },
				func() error { return b.CompareAndSwap(t.Context(), identity.CurrentKey(), "", []byte("test")) },
			}
			for _, op := range operations {
				err := op()
				if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
					t.Fatalf("mapping: %v", err)
				}
				if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "offline") {
					t.Fatalf("unsafe diagnostic: %v", err)
				}
			}
		})
	}
}

func TestKeyAndInputConfinement(t *testing.T) {
	var calls int
	b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; protocolError(w, 500, "InternalError") }), time.Second)
	obj, manifest := payloadKeys()
	invalid := []string{"", "v3/current.json", "/" + identity.CurrentKey(), "other/" + identity.CurrentKey(), identity.BaseKey() + "/../current.json", strings.Replace(identity.CurrentKey(), "/sessions/", "//sessions/", 1), identity.BaseKey() + "/objects/no.zst", strings.Replace(identity.CurrentKey(), "/archives/61726368697665/", "/archives/AB/", 1), identity.CurrentKey() + "?x=y", identity.BaseKey() + "/objects/" + strings.Repeat("A", 64) + ".zst", "capture-v1/archive.json/", "capture-v1/archives/00/machines/00/agents/00/sessions//current.json"}
	for _, key := range invalid {
		if _, err := b.Open(t.Context(), key); err == nil {
			t.Errorf("Open accepted %q", key)
		}
		if _, _, err := b.Current(t.Context(), key); err == nil {
			t.Errorf("Current accepted %q", key)
		}
		if err := b.PutFile(t.Context(), key, "absent"); err == nil {
			t.Errorf("PutFile accepted %q", key)
		}
		if err := b.CompareAndSwap(t.Context(), key, "", nil); err == nil {
			t.Errorf("CAS accepted %q", key)
		}
	}
	for _, key := range []string{obj, manifest} {
		if _, _, err := b.Current(t.Context(), key); err == nil {
			t.Fatal("payload accepted as reference")
		}
		if err := b.CompareAndSwap(t.Context(), key, "", nil); err == nil {
			t.Fatal("CAS payload accepted")
		}
	}
	if err := b.PutFile(t.Context(), identity.CurrentKey(), "absent"); err == nil {
		t.Fatal("PutFile reference accepted")
	}
	for _, token := range []string{"unquoted", "*", "\"bad\r\nHeader: value\"", "\"nested\"quote\""} {
		if err := b.CompareAndSwap(t.Context(), identity.CurrentKey(), token, nil); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	if err := b.CompareAndSwap(t.Context(), publication.ArchiveKey, `"valid"`, nil); err == nil {
		t.Fatal("archive replacement accepted")
	}
	if err := b.CompareAndSwap(t.Context(), identity.CurrentKey(), "", make([]byte, publication.MaxMetadata+1)); err == nil {
		t.Fatal("oversize CAS accepted")
	}
	if calls != 0 {
		t.Fatalf("invalid input made %d requests", calls)
	}
}

func TestConstructor(t *testing.T) {
	client := clientFor(t, newProtocol())
	for _, tc := range []struct {
		client  *s3.Client
		bucket  string
		timeout time.Duration
	}{
		{nil, "bucket", time.Second}, {client, "", time.Second}, {client, "bucket", 0}, {client, "bucket", -time.Second}, {client, "bucket/key", time.Second}, {client, " bucket", time.Second}, {client, "bucket\n", time.Second},
	} {
		if _, err := New(tc.client, tc.bucket, tc.timeout); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestMetadataLimitsAndMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   []byte
		length string
		etag   string
	}{
		{"limit", make([]byte, publication.MaxMetadata), strconv.Itoa(publication.MaxMetadata), `"version"`},
		{"over", make([]byte, publication.MaxMetadata+1), strconv.Itoa(publication.MaxMetadata + 1), `"version"`},
		{"short", []byte("tiny"), "100", `"version"`},
		{"missing-token", []byte("ok"), "2", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", tc.length)
				if tc.etag != "" {
					w.Header().Set("ETag", tc.etag)
				}
				_, _ = w.Write(tc.body)
			}), 5*time.Second)
			data, _, err := b.Current(t.Context(), identity.CurrentKey())
			if tc.name == "limit" {
				if err != nil || len(data) != publication.MaxMetadata {
					t.Fatalf("exact limit: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestStreamingDeadlineAndCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelParent), func(t *testing.T) {
			b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "100")
				w.Header().Set("ETag", `"version"`)
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}), 150*time.Millisecond)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r, err := b.Open(ctx, identity.CurrentKey())
			if err != nil {
				t.Fatal(err)
			}
			if cancelParent {
				cancel()
			}
			_, err = io.ReadAll(r)
			if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				t.Fatalf("stream deadline not preserved: %v", err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOperationDeadlines(t *testing.T) {
	b := backendFor(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }), 100*time.Millisecond)
	if _, _, err := b.Current(t.Context(), identity.CurrentKey()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Current: %v", err)
	}
	if err := b.CompareAndSwap(t.Context(), identity.CurrentKey(), "", []byte("a")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CAS: %v", err)
	}
}

func TestPayloadBounds(t *testing.T) {
	b := backendFor(t, newProtocol(), time.Second)
	obj, manifest := payloadKeys()
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile(t.Context(), obj, fifo); err == nil {
		t.Fatal("FIFO accepted")
	}
	path := filepath.Join(t.TempDir(), "sparse")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxObjectSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile(t.Context(), obj, path); err == nil {
		t.Fatal("oversize object accepted")
	}
	if err := os.Truncate(path, publication.MaxMetadata+1); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile(t.Context(), manifest, path); err == nil {
		t.Fatal("oversize metadata accepted")
	}
	if err := b.PutFile(t.Context(), obj, t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	if err := b.PutFile(t.Context(), obj, path+"missing"); err == nil {
		t.Fatal("missing file accepted")
	}
}

// The server commits, then drops the response. The transport must not claim
// success or retry CAS; publication-layer reconciliation owns that ambiguity.
func TestLostAcknowledgementIsNotSuccess(t *testing.T) {
	p := newProtocol()
	var mu sync.Mutex
	puts := 0
	b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			p.ServeHTTP(w, r)
			return
		}
		// Record the commit via the same protocol implementation without sending it.
		recorder := httptest.NewRecorder()
		p.ServeHTTP(recorder, r)
		mu.Lock()
		puts++
		mu.Unlock()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}), time.Second)
	// Even a client configured for retries must not replay an ambiguous CAS.
	b.client = s3.New(b.client.Options(), func(o *s3.Options) { o.RetryMaxAttempts = 4 })
	err := b.CompareAndSwap(t.Context(), identity.CurrentKey(), "", []byte("committed"))
	if err == nil {
		t.Fatal("lost acknowledgement reported success")
	}
	body, _, err := b.Current(t.Context(), identity.CurrentKey())
	if err != nil || string(body) != "committed" {
		t.Fatalf("readback: %q %v", body, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if puts != 1 {
		t.Fatalf("ambiguous CAS retried %d times", puts)
	}
}

func TestImmutablePutProtocol(t *testing.T) {
	data := []byte("immutable offline evidence")
	obj, manifest := payloadKeys(data)
	for _, key := range []string{obj, manifest} {
		t.Run(key[strings.LastIndex(key, ".")+1:], func(t *testing.T) {
			p := newProtocol()
			b := backendFor(t, p, time.Second)
			path := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := b.PutFile(t.Context(), key, path); err != nil {
					t.Fatal(err)
				}
			}
			p.mu.Lock()
			before := len(p.requests)
			p.mu.Unlock()
			if err := os.WriteFile(path, []byte("different bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := b.PutFile(t.Context(), key, path); !errors.Is(err, publication.ErrConflict) {
				t.Fatalf("different file: %v", err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.requests) != before || !bytes.Equal(p.objects[key].body, data) {
				t.Fatal("digest mismatch made a network call or changed stored bytes")
			}
			if len(p.requests) != 3 || p.requests[0].method != http.MethodPut || p.requests[1].method != http.MethodPut || p.requests[2].method != http.MethodGet {
				t.Fatalf("unexpected immutable request sequence: %+v", p.requests)
			}
			assertImmutableRequests(t, p.requests)
		})
	}
}

func assertImmutableRequests(t *testing.T, requests []request) {
	t.Helper()
	for _, req := range requests {
		switch req.method {
		case http.MethodPut:
			if req.none != "*" || req.match != "" {
				t.Fatalf("forbidden immutable overwrite: %+v", req)
			}
		case http.MethodGet:
		default:
			t.Fatalf("forbidden immutable operation: %+v", req)
		}
	}
}

func TestImmutableCorruptExisting(t *testing.T) {
	data := []byte("correct content")
	obj, manifest := payloadKeys(data)
	for _, key := range []string{obj, manifest} {
		for _, corrupt := range [][]byte{[]byte("corrupt content"), []byte("short")} {
			p := newProtocol()
			p.objects[key] = stored{corrupt, `"opaque"`}
			b := backendFor(t, p, time.Second)
			path := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := b.PutFile(t.Context(), key, path); !errors.Is(err, publication.ErrConflict) {
				t.Fatalf("corrupt existing: %v", err)
			}
			p.mu.Lock()
			if !bytes.Equal(p.objects[key].body, corrupt) {
				t.Fatal("corruption was overwritten")
			}
			assertImmutableRequests(t, p.requests)
			p.mu.Unlock()
		}
	}
}

func TestImmutableLostAcknowledgement(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(strconv.FormatBool(corrupt), func(t *testing.T) {
			data := []byte("lost acknowledgement fixture")
			key, _ := payloadKeys(data)
			p := newProtocol()
			b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					p.ServeHTTP(w, r)
					return
				}
				p.ServeHTTP(httptest.NewRecorder(), r)
				if corrupt {
					p.mu.Lock()
					p.objects[key] = stored{bytes.Repeat([]byte("x"), len(data)), `"opaque"`}
					p.mu.Unlock()
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}), time.Second)
			b.client = s3.New(b.client.Options(), func(o *s3.Options) { o.RetryMaxAttempts = 4 })
			path := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			err := b.PutFile(t.Context(), key, path)
			if corrupt && !errors.Is(err, publication.ErrConflict) || !corrupt && err != nil {
				t.Fatalf("lost ack reconciliation: %v", err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.requests) != 2 || p.requests[0].method != http.MethodPut || p.requests[1].method != http.MethodGet {
				t.Fatalf("ambiguous immutable PUT replayed: %+v", p.requests)
			}
			assertImmutableRequests(t, p.requests)
		})
	}
}
