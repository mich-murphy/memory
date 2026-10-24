package capturebackend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/publicationr2"
)

type countedErrorBody struct {
	read, closes      int
	readErr, closeErr error
}

func (b *countedErrorBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil // Intentionally infinite: a faulty drain would hang the test.
}
func (b *countedErrorBody) Close() error { b.closes++; return b.closeErr }

func writeErrorTestPayload(t *testing.T) (string, string) {
	t.Helper()
	raw := []byte("synthetic")
	sum := sha256.Sum256(raw)
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, hex.EncodeToString(sum[:])
}

func TestBoundedErrorClientOverflow(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := &countedErrorBody{}
			client := boundedErrorClient{client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: body, ContentLength: -1, Header: make(http.Header)}, nil
			})}}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://synthetic.invalid", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(req)
			if response != nil {
				if closeErr := response.Body.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if response == nil || response.StatusCode != status || response.Body != http.NoBody || !errors.Is(err, errErrorResponseTooLarge) {
				t.Fatal("overflow did not return fixed failure")
			}
			if body.read != maxErrorResponseBytes+1 || body.closes != 1 {
				t.Fatalf("read=%d close=%d", body.read, body.closes)
			}
		})
	}
}

func TestFactoryOversizedErrorsBoundedBeforeSDK(t *testing.T) {
	for _, useDefaultTransport := range []bool{false, true} {
		t.Run(map[bool]string{false: "injected-client", true: "default-client-transport"}[useDefaultTransport], func(t *testing.T) {
			for _, operation := range []string{"current", "open", "cas", "put"} {
				t.Run(operation, func(t *testing.T) {
					body := &countedErrorBody{}
					calls := 0
					rt := roundTrip(func(*http.Request) (*http.Response, error) {
						calls++
						return &http.Response{StatusCode: http.StatusForbidden, Body: body, ContentLength: -1, Header: make(http.Header)}, nil
					})
					var client aws.HTTPClient = &http.Client{Transport: rt}
					if useDefaultTransport {
						c := defaultHTTPClient()
						c.Transport = rt
						client = c
					}
					d := deps(rt)
					d.HTTPClient = client
					b, err := Open(t.Context(), spec(), d)
					if err != nil {
						t.Fatal(err)
					}
					switch operation {
					case "current":
						_, _, err = b.Current(t.Context(), identity().CurrentKey())
					case "open":
						_, err = b.Open(t.Context(), identity().CurrentKey())
					case "cas":
						err = b.CompareAndSwap(t.Context(), identity().CurrentKey(), "", []byte("synthetic"))
					case "put":
						// Auth failure must remain destination-fatal, without reconciliation.
						path, digest := writeErrorTestPayload(t)
						err = b.PutFile(t.Context(), identity().BaseKey()+"/manifests/"+digest+".json", path)
					}
					if !errors.Is(err, publicationr2.ErrDestinationPaused) {
						t.Fatalf("unsafe or missing public failure: %v", err)
					}
					wantCalls := 1 // Authentication failures must pause without PUT reconciliation.
					if calls != wantCalls || body.read != calls*(maxErrorResponseBytes+1) || body.closes != calls {
						t.Fatalf("unbounded SDK path: calls=%d read=%d closes=%d", calls, body.read, body.closes)
					}
				})
			}
		})
	}
}

func TestBoundedErrorsPreserveStatusClassification(t *testing.T) {
	for code, want := range map[int]error{401: publicationr2.ErrDestinationPaused, 403: publicationr2.ErrDestinationPaused, 404: publication.ErrNotFound, 409: publication.ErrConflict, 412: publication.ErrConflict} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			closed := 0
			b := factory(t, roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Body: &closeCountBody{Reader: strings.NewReader("<Error><Code>AccessDenied</Code><Message>PRIVATE_BODY</Message></Error>"), closed: &closed}, Header: make(http.Header)}, nil
			}))
			_, _, err := b.Current(t.Context(), identity().CurrentKey())
			if !errors.Is(err, want) || strings.Contains(err.Error(), "PRIVATE") || closed != 1 {
				t.Fatal("bounded status classification or closure changed")
			}
		})
	}
}

type closeCountBody struct {
	io.Reader
	closed *int
}

func (b *closeCountBody) Close() error { (*b.closed)++; return nil }

func TestBoundedErrorClientPreservesSuccessStream(t *testing.T) {
	for _, code := range []int{200, 206, 299} {
		body := &countedErrorBody{}
		original := &http.Response{StatusCode: code, Body: body, ContentLength: 3 * maxErrorResponseBytes, Header: make(http.Header)}
		client := boundedErrorClient{client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return original, nil })}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://synthetic.invalid", nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := client.Do(req)
		if err != nil || out != original || out.Body != body || body.read != 0 || body.closes != 0 {
			t.Fatal("successful stream buffered or replaced")
		}
		got, err := io.ReadAll(io.LimitReader(out.Body, 2*maxErrorResponseBytes))
		if err != nil || len(got) != 2*maxErrorResponseBytes {
			t.Fatal("successful body truncated")
		}
		if err := out.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if body.closes != 1 {
			t.Fatal("successful stream closure changed")
		}
	}
}

func TestBoundedErrorClientExactLimitAndFailures(t *testing.T) {
	for _, size := range []int{0, maxErrorResponseBytes - 1, maxErrorResponseBytes} {
		closed := 0
		raw := bytes.Repeat([]byte("x"), size)
		client := boundedErrorClient{client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Body: &closeCountBody{Reader: bytes.NewReader(raw), closed: &closed}}, nil
		})}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://synthetic.invalid", nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(out.Body)
		if err != nil || !bytes.Equal(got, raw) || closed != 1 {
			t.Fatal("bounded error bytes changed")
		}
		if err := out.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if closed != 1 {
			t.Fatal("original body closed twice")
		}
	}
	for _, body := range []*countedErrorBody{{readErr: errors.New("PRIVATE_READ")}, {readErr: io.EOF, closeErr: errors.New("PRIVATE_CLOSE")}} {
		client := boundedErrorClient{client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: body}, nil
		})}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://synthetic.invalid", nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := client.Do(req)
		if out != nil {
			if closeErr := out.Body.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		if out == nil || out.StatusCode != http.StatusInternalServerError || out.Body != http.NoBody || err == nil || strings.Contains(err.Error(), "PRIVATE") || body.closes != 1 {
			t.Fatal("error body failure leaked or failed to close")
		}
	}
}
