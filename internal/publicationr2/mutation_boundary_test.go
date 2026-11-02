package publicationr2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/mich-murphy/memory/internal/publication"
)

type trackedBody struct {
	io.Reader
	closed atomic.Int32
}

func (b *trackedBody) Close() error { b.closed.Add(1); return nil }

// syntheticBackend serves every request from fn without a network listener.
func syntheticBackend(t *testing.T, fn listHTTPFunc) *Backend {
	t.Helper()
	client := s3.New(s3.Options{
		Region: "auto", BaseEndpoint: aws.String("https://synthetic.invalid"), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("offline-key", "offline-secret", ""),
		HTTPClient:  fn, RetryMaxAttempts: 1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	b, err := New(client, "offline-bucket", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func declaredResponse(declared int64, body io.ReadCloser) listHTTPFunc {
	return func(*http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Content-Length", strconv.FormatInt(declared, 10))
		h.Set("ETag", `"version"`)
		return &http.Response{StatusCode: http.StatusOK, Header: h, ContentLength: declared, Body: body}, nil
	}
}

func keysFor(data []byte) (object, manifest string) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	return identity.BaseKey() + "/objects/" + digest + ".zst", identity.BaseKey() + "/manifests/" + digest + ".json"
}

// Key length parity differs by kind, so the exact 1024-byte boundary is only
// reachable for some kinds; sweep session lengths and require that a key of
// exactly 1024 bytes was seen and accepted, and 1025 rejected.
func TestKeyLengthBoundary(t *testing.T) {
	digest := strings.Repeat("a", 64)
	seen := map[int]bool{}
	for n := 1; n < 600; n++ {
		id := identity
		id.SessionID = strings.Repeat("s", n)
		for _, key := range []string{id.CurrentKey(), id.BaseKey() + "/objects/" + digest + ".zst", id.BaseKey() + "/manifests/" + digest + ".json"} {
			if got, want := keyType(key) != invalidKey, len(key) <= 1024; got != want {
				t.Fatalf("key length %d accepted=%v want %v", len(key), got, want)
			}
			seen[len(key)] = true
		}
	}
	if !seen[1024] || !seen[1025] || !seen[1023] {
		t.Fatalf("sweep did not reach the boundary: 1023=%v 1024=%v 1025=%v", seen[1023], seen[1024], seen[1025])
	}
}

func TestClientErrorClassBoundary(t *testing.T) {
	for _, tc := range []struct {
		status   int
		rejected bool
	}{{399, false}, {400, true}, {499, true}, {500, false}} {
		b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { protocolError(w, tc.status, "Synthetic") }), time.Second)
		err := b.CompareAndSwap(t.Context(), identity.CurrentKey(), "", []byte("x"))
		if err == nil || errors.Is(err, publication.ErrRejected) != tc.rejected {
			t.Fatalf("status %d: rejected=%v err=%v", tc.status, errors.Is(err, publication.ErrRejected), err)
		}
	}
}

func TestOpenLengthBoundaries(t *testing.T) {
	_, manifest := payloadKeys()
	for _, size := range []int{0, publication.MaxMetadata} {
		b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(size))
			_, _ = w.Write(make([]byte, size)) // HTTP test peer.
		}), 10*time.Second)
		r, err := b.Open(t.Context(), manifest)
		if err != nil {
			t.Fatalf("declared length %d: %v", size, err)
		}
		data, err := io.ReadAll(r)
		if err != nil || len(data) != size {
			t.Fatalf("declared length %d: read %d bytes, err %v", size, len(data), err)
		}
		if err = r.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenClosesBodyOfInvalidLengthResponse(t *testing.T) {
	_, manifest := payloadKeys()
	body := &trackedBody{Reader: strings.NewReader("x")}
	b := syntheticBackend(t, declaredResponse(publication.MaxMetadata+1, body))
	if _, err := b.Open(t.Context(), manifest); err == nil || err.Error() != "invalid publication response length" {
		t.Fatalf("Open: %v", err)
	}
	if got := body.closed.Load(); got != 1 {
		t.Fatalf("rejected response body closed %d times", got)
	}
}

func TestCurrentChecksDeclaredLengthAgainstBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared int64
		ok       bool
	}{{"match", 10, true}, {"declared-shorter", 4, false}, {"declared-longer", 11, false}} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticBackend(t, declaredResponse(tc.declared, io.NopCloser(strings.NewReader("0123456789"))))
			data, token, err := b.Current(t.Context(), identity.CurrentKey())
			if tc.ok {
				if err != nil || string(data) != "0123456789" || token != `"version"` {
					t.Fatalf("Current: %q %q %v", data, token, err)
				}
				return
			}
			if err == nil || err.Error() != "invalid publication metadata length" {
				t.Fatalf("Current: %v", err)
			}
		})
	}
}

func TestStreamReadFailureIsRedacted(t *testing.T) {
	_, manifest := payloadKeys()
	b := backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("0123456789")) // Handler returns short: the peer drops the body.
	}), 5*time.Second)
	r, err := b.Open(t.Context(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(r); err == nil || err.Error() != "publication destination operation failed" {
		t.Fatalf("read failure: %v", err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidTokenBoundaries(t *testing.T) {
	quote := func(s string) string { return `"` + s + `"` }
	for _, tc := range []struct {
		name  string
		token string
		want  bool
	}{
		{"empty-quotes", `""`, false},
		{"one-char", quote("a"), true},
		{"first-printable", quote("!"), true},
		{"space", quote(" "), false},
		{"last-printable", quote("~"), true},
		{"delete", quote("\x7f"), false},
		{"max-length", quote(strings.Repeat("a", 1022)), true},
		{"over-length", quote(strings.Repeat("a", 1023)), false},
	} {
		if got := validToken(tc.token); got != tc.want {
			t.Errorf("%s: validToken=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestCompareAndSwapBodyLimitBoundary(t *testing.T) {
	p := newProtocol()
	b := backendFor(t, p, 10*time.Second)
	if err := b.CompareAndSwap(t.Context(), identity.CurrentKey(), "", make([]byte, publication.MaxMetadata)); err != nil {
		t.Fatalf("body at limit: %v", err)
	}
	data, _, err := b.Current(t.Context(), identity.CurrentKey())
	if err != nil || len(data) != publication.MaxMetadata {
		t.Fatalf("stored %d bytes: %v", len(data), err)
	}
	if err = b.CompareAndSwap(t.Context(), identity.BaseKey()+"/x/current.json", "", nil); err == nil {
		t.Fatal("non-canonical reference key accepted")
	}
}

func TestPutFileSizeBoundaries(t *testing.T) {
	big := make([]byte, publication.MaxMetadata+1)
	atLimit := big[:publication.MaxMetadata]
	cases := []struct {
		name string
		key  func(object, manifest string) string
		data []byte
	}{
		{"empty-object", func(o, _ string) string { return o }, []byte{}},
		{"manifest-at-limit", func(_, m string) string { return m }, atLimit},
		{"object-above-metadata-limit", func(o, _ string) string { return o }, big},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newProtocol()
			b := backendFor(t, p, 30*time.Second)
			object, manifest := keysFor(tc.data)
			key := tc.key(object, manifest)
			if err := b.PutFile(t.Context(), key, writePayload(t, tc.data)); err != nil {
				t.Fatalf("PutFile: %v", err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if got, ok := p.objects[key]; !ok || len(got.body) != len(tc.data) {
				t.Fatalf("stored %d bytes, present=%v, want %d", len(got.body), ok, len(tc.data))
			}
		})
	}
}

// blockingHandler holds a request open until the returned release is called or
// the peer goes away. Callers must register release with t.Cleanup AFTER the
// server exists: cleanups run last-in first-out and server.Close waits for handlers.
func blockingHandler(serve func(http.ResponseWriter, *http.Request) bool) (http.Handler, func()) {
	release := make(chan struct{})
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serve(w, r) {
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}), func() { once.Do(func() { close(release) }) }
}

func stallBudget() BodyBudget {
	return BodyBudget{Base: 5 * time.Second, MinThroughput: 1 << 20, Floor: 5 * time.Second, Cap: 5 * time.Second, Idle: 200 * time.Millisecond}
}

// A server that stops consuming a large object upload is detected by the idle
// guard, well before the total body budget, and reported as a retryable stall.
func TestObjectUploadStallIsDetectedByIdleGuard(t *testing.T) {
	data := make([]byte, 16<<20)
	object, _ := keysFor(data)
	h, release := blockingHandler(func(http.ResponseWriter, *http.Request) bool { return false })
	b := budgetBackend(t, h, time.Minute, stallBudget())
	t.Cleanup(release)
	start := time.Now()
	err := b.PutFile(t.Context(), object, writePayload(t, data))
	if !errors.Is(err, errStalled) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 4*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

// After an ambiguous object PUT the read-back verification carries its own
// idle guard, so a stalled read-back is a retryable stall, not a full-budget wait.
func TestObjectReconcileStallIsDetectedByIdleGuard(t *testing.T) {
	data := []byte("reconcile read-back stalls after a partial body")
	object, _ := keysFor(data)
	h, release := blockingHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut {
			protocolError(w, http.StatusInternalServerError, "InternalError")
			return true
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data[:3]) // HTTP test peer.
		w.(http.Flusher).Flush()
		return false
	})
	b := budgetBackend(t, h, time.Minute, stallBudget())
	t.Cleanup(release)
	start := time.Now()
	err := b.PutFile(t.Context(), object, writePayload(t, data))
	if !errors.Is(err, errStalled) || time.Since(start) > 4*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestListResponseBoundStatusAndSizeBoundaries(t *testing.T) {
	for _, status := range []int{199, 300} {
		body := &infiniteListBody{}
		client := boundedListClient{client: listHTTPFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body}, nil
		})}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://synthetic.invalid", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if err != nil || response == nil || response.Body != io.ReadCloser(body) || body.read != 0 || body.closed != 0 {
			t.Fatalf("status %d: passthrough err=%v read=%d closed=%d", status, err, body.read, body.closed)
		}
		if err = response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	exact := bytes.Repeat([]byte("x"), MaxListResponseBytes)
	client := boundedListClient{client: listHTTPFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(exact))}, nil
	})}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://synthetic.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("response of exactly the limit: %v", err)
	}
	got, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || !bytes.Equal(got, exact) {
		t.Fatalf("limit-sized body: %d bytes, %v", len(got), err)
	}
}

func listXML(truncated bool, prefixes ...string) string {
	var sb strings.Builder
	sb.WriteString("<ListBucketResult><IsTruncated>" + strconv.FormatBool(truncated) + "</IsTruncated>")
	for _, p := range prefixes {
		sb.WriteString("<CommonPrefixes><Prefix>" + p + "</Prefix></CommonPrefixes>")
	}
	sb.WriteString("</ListBucketResult>")
	return sb.String()
}

func TestListNamespaceLimitsAndAdvancement(t *testing.T) {
	session := func(s string) string {
		id := identity
		id.SessionID = s
		return id.BaseKey() + "/"
	}
	a, b2 := session("a"), session("b")
	serve := func(body string) *Backend {
		return backendFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }), time.Second)
	}
	for _, limit := range []int{0, 257} {
		if _, _, err := serve(listXML(false)).ListSessionNamespaces(t.Context(), identity, "", limit); err == nil || err.Error() != "invalid namespace listing options" {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	for _, limit := range []int{1, 256} {
		got, done, err := serve(listXML(false)).ListSessionNamespaces(t.Context(), identity, "", limit)
		if err != nil || len(got) != 0 || !done {
			t.Fatalf("limit %d: %v %v %v", limit, got, done, err)
		}
	}
	got, done, err := serve(listXML(false, a, b2)).ListSessionNamespaces(t.Context(), identity, "", 2)
	if err != nil || !done || len(got) != 2 || got[0] != a || got[1] != b2 {
		t.Fatalf("ascending: %v %v %v", got, done, err)
	}
	got, done, err = serve(listXML(true, b2)).ListSessionNamespaces(t.Context(), identity, a, 2)
	if err != nil || done || len(got) != 1 || got[0] != b2 {
		t.Fatalf("truncated: %v %v %v", got, done, err)
	}
	for name, tc := range map[string]struct {
		after string
		list  []string
	}{
		"equals-cursor": {a, []string{a}},
		"duplicate":     {"", []string{a, a}},
		"descending":    {"", []string{b2, a}},
	} {
		if _, _, err = serve(listXML(false, tc.list...)).ListSessionNamespaces(t.Context(), identity, tc.after, 2); err == nil || err.Error() != "nonadvancing namespace listing" {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
