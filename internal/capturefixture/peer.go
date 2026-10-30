// Package capturefixture provides a bounded, non-network synthetic S3 peer.
// It is a fixture transport, not a storage backend or an R2 emulator.
package capturefixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	Host    = "00000000000000000000000000000000.r2.cloudflarestorage.com"
	Bucket  = "synthetic-fixture"
	MaxBody = 16 << 20
)

type object struct {
	body []byte
	etag string
}

// Peer owns immutable byte copies behind a mutex. No request can reach a socket.
// LostACK injects one transport failure after committing a current reference.
type Peer struct {
	mu         sync.Mutex
	objects    map[string]object
	LostACK    bool
	lost       bool
	gets, puts int
}

// Counts returns successful reads and committed writes, plus lost-ACK injection.
func (p *Peer) Counts() (int, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gets, p.puts, p.lost
}

func response(r *http.Request, status int, body []byte, etag string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if etag != "" {
		h.Set("ETag", etag)
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: h, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}
}

// RoundTrip accepts only bounded GET/conditional PUT object requests. Diagnostics
// are fixed strings: neither source paths, request keys nor bodies are reported.
func (p *Peer) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	if err := validateRequest(r); err != nil {
		return nil, err
	}
	key := strings.TrimPrefix(r.URL.Path, "/"+Bucket+"/")
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	old, exists := p.objects[key]
	if r.Method == http.MethodGet {
		if !exists {
			return response(r, http.StatusNotFound, nil, ""), nil
		}
		p.gets++
		return response(r, http.StatusOK, old.body, old.etag), nil
	}
	none, match := r.Header.Get("If-None-Match"), r.Header.Get("If-Match")
	mutable := strings.HasSuffix(key, "/current.json")
	if (none != "*" || match != "") && (none != "" || match == "" || !mutable) {
		return nil, errors.New("fixture requires conditional write")
	}
	if (none == "*" && exists) || (match != "" && (!exists || old.etag != match)) {
		return response(r, http.StatusPreconditionFailed, nil, ""), nil
	}
	if p.objects == nil {
		p.objects = make(map[string]object)
	}
	p.puts++
	etag := fmt.Sprintf("\"opaque-fixture-%d-token\"", p.puts)
	p.objects[key] = object{body: bytes.Clone(body), etag: etag}
	if p.LostACK && !p.lost && mutable {
		p.lost = true
		return nil, errors.New("synthetic acknowledgement lost")
	}
	return response(r, http.StatusOK, nil, etag), nil
}

func readBody(r *http.Request) ([]byte, error) {
	var body []byte
	if r.Method == http.MethodPut {
		if r.Body == nil || r.ContentLength < 0 || r.ContentLength > MaxBody {
			return nil, errors.New("invalid fixture body length")
		}
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
		if err != nil {
			return nil, errors.New("fixture body read failed")
		}
		if int64(len(body)) != r.ContentLength || len(body) > MaxBody {
			return nil, errors.New("fixture body length mismatch")
		}
		sum := sha256.Sum256(body)
		if hash := r.Header.Get("X-Amz-Content-Sha256"); hash != "UNSIGNED-PAYLOAD" && hash != hex.EncodeToString(sum[:]) {
			return nil, errors.New("fixture body hash mismatch")
		}
	}

	return body, nil
}

func validateRequest(r *http.Request) error {
	if r.URL.Scheme != "https" || r.URL.Host != Host || (r.URL.RawQuery != "" && r.URL.RawQuery != "x-id=GetObject" && r.URL.RawQuery != "x-id=PutObject") || r.URL.ForceQuery || r.URL.Fragment != "" || !strings.HasPrefix(r.URL.Path, "/"+Bucket+"/") || len(r.URL.Path) <= len(Bucket)+2 {
		return errors.New("unsupported fixture request")
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		return errors.New("unsupported fixture operation")
	}

	return nil
}
