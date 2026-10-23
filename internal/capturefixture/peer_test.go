package capturefixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

func request(t *testing.T, method, key, body, none, match string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, "https://"+Host+"/"+Bucket+"/"+key, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	r.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sum[:]))
	r.Header.Set("If-None-Match", none)
	r.Header.Set("If-Match", match)
	return r
}

func round(t *testing.T, p *Peer, r *http.Request, status int) string {
	t.Helper()
	out, err := p.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := out.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if out.StatusCode != status {
		t.Fatalf("status %d want %d", out.StatusCode, status)
	}
	return out.Header.Get("ETag")
}

func TestConditionalAndLostACK(t *testing.T) {
	p := &Peer{LostACK: true}
	token := round(t, p, request(t, http.MethodPut, "immutable", "one", "*", ""), 200)
	if token == "" || token == `"one"` {
		t.Fatal("not opaque token")
	}
	round(t, p, request(t, http.MethodPut, "immutable", "two", "*", ""), 412)
	r := request(t, http.MethodPut, "session/current.json", "first", "*", "")
	if out, err := p.RoundTrip(r); out != nil || err == nil {
		if out != nil {
			if closeErr := out.Body.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		t.Fatal("lost ACK not injected")
	}
	token = round(t, p, request(t, http.MethodGet, "session/current.json", "", "", ""), 200)
	round(t, p, request(t, http.MethodPut, "session/current.json", "stale", "", `"stale"`), 412)
	newer := round(t, p, request(t, http.MethodPut, "session/current.json", "second", "", token), 200)
	if newer == token {
		t.Fatal("token not advanced")
	}
	out, err := p.RoundTrip(request(t, http.MethodGet, "session/current.json", "", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, []byte("second")) {
		t.Fatal("wrong committed bytes")
	}
	round(t, p, request(t, http.MethodGet, "missing", "", "", ""), 404)
	gets, puts, lost := p.Counts()
	if gets != 2 || puts != 3 || !lost {
		t.Fatalf("unexpected counts %d %d %v", gets, puts, lost)
	}
}

func TestRejectRequests(t *testing.T) {
	for _, tc := range []struct{ name, method, key, none, match string }{
		{"head", http.MethodHead, "key", "", ""},
		{"delete", http.MethodDelete, "key", "", ""},
		{"list", http.MethodGet, "?list-type=2", "", ""},
		{"multipart", http.MethodPost, "key?uploads", "", ""},
		{"unconditional", http.MethodPut, "session/current.json", "", ""},
		{"immutable overwrite", http.MethodPut, "immutable", "", `"token"`},
		{"double guard", http.MethodPut, "session/current.json", "*", `"token"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Peer{}
			if out, err := p.RoundTrip(request(t, tc.method, tc.key, "private body", tc.none, tc.match)); err == nil || out != nil {
				if out != nil {
					if closeErr := out.Body.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				t.Fatal("accepted unsupported request")
			}
		})
	}
}

func TestBoundsHashAndCancel(t *testing.T) {
	for _, mode := range []string{"length", "overflow", "hash", "nil", "host", "cancel", "read"} {
		t.Run(mode, func(t *testing.T) {
			r := request(t, http.MethodPut, "immutable", "private body", "*", "")
			switch mode {
			case "length":
				r.ContentLength++
			case "overflow":
				r.ContentLength = MaxBody + 1
			case "hash":
				r.Header.Set("X-Amz-Content-Sha256", "invalid")
			case "nil":
				r.Body = nil
			case "host":
				r.URL.Host = "localhost"
			case "cancel":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				r = r.WithContext(ctx)
			case "read":
				r.Body = io.NopCloser(failedReader{})
			}
			if out, err := (&Peer{}).RoundTrip(r); err == nil || out != nil {
				if out != nil {
					if closeErr := out.Body.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				t.Fatal("accepted invalid request")
			} else if strings.Contains(err.Error(), "private body") {
				t.Fatal("body leaked")
			}
		})
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
