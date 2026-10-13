package store_test

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/store"
)

// sink answers S3 requests without keeping anything, so the R2 benchmarks measure the client:
// hashing, signing and sending.
func sink(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("ETag", `"etag"`)
	var reply string
	switch {
	case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
		reply = `<InitiateMultipartUploadResult><UploadId>up</UploadId></InitiateMultipartUploadResult>`
	case r.Method == http.MethodPost:
		reply = `<CompleteMultipartUploadResult><ETag>"etag"</ETag></CompleteMultipartUploadResult>`
	}
	_, _ = io.WriteString(w, reply) // a failed reply fails the client's request, which the benchmark reports
}

func BenchmarkR2Put(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(sink))
	b.Cleanup(srv.Close)
	s, err := store.NewR2(&config.Config{
		R2Endpoint: srv.URL, R2Bucket: "sessions", R2AccessKeyID: "id", R2SecretAccessKey: "secret",
	})
	if err != nil {
		b.Fatal(err)
	}
	meta := map[string]string{"sha256": "x", "agent": "claude", "machine": "m", "role": "main", "start": "0", "end": "1"}
	for _, size := range []int{1 << 10, 64 << 10, 4 << 20} {
		b.Run(fmt.Sprintf("put/%dKiB", size>>10), func(b *testing.B) {
			body := make([]byte, size)
			if _, err := rand.Read(body); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(size))
			for b.Loop() {
				if err := s.Put(b.Context(), "raw/key.zst", body, meta); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("multipart/80MiB", func(b *testing.B) {
		body := make([]byte, 80<<20)
		if _, err := rand.Read(body); err != nil {
			b.Fatal(err)
		}
		path := filepath.Join(b.TempDir(), "big.zst")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			b.Fatal(err)
		}
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			if err := s.PutFile(b.Context(), "raw/big.zst", path, meta); err != nil {
				b.Fatal(err)
			}
		}
	})
}
