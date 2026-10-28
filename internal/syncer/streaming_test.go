package syncer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

type repeatingInput struct {
	pattern []byte
	offset  int
}

func (r *repeatingInput) Read(buffer []byte) (int, error) {
	// Bulk copies keep the 64 MiB fixture cheap under race/coverage instrumentation.
	// The suffix completes the current pattern; doubling then repeats whole cycles.
	prefix := copy(buffer, r.pattern[r.offset:])
	filled := prefix
	if filled < len(buffer) {
		filled += copy(buffer[filled:], r.pattern)
		for filled < len(buffer) {
			filled += copy(buffer[filled:], buffer[prefix:filled])
		}
	}
	r.offset = (r.offset + len(buffer)) % len(r.pattern)
	return len(buffer), nil
}

func TestLargePlainAndCompressedCaptureHasBoundedHeap(t *testing.T) {
	const length int64 = 64 << 20
	old := largeFileSize
	largeFileSize = 1 << 20
	t.Cleanup(func() { largeFileSize = old })
	for _, compressed := range []bool{false, true} {
		name := "plain"
		if compressed {
			name = "zstd"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "rollout.jsonl")
			if compressed {
				path += ".zst"
			}
			source, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.New()
			input := &repeatingInput{pattern: []byte("{\"message\":\"bounded streaming\"}\n")}
			if compressed {
				err = raw.CompressStream(input, source, length, digest)
			} else {
				_, err = io.CopyN(io.MultiWriter(source, digest), input, length)
			}
			if closeErr := source.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := hex.EncodeToString(digest.Sum(nil))
			cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
			bucket := newMemStore()
			db, err := state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			runtime.GC()
			var start runtime.MemStats
			runtime.ReadMemStats(&start)
			var peak atomic.Uint64
			peak.Store(start.HeapAlloc)
			stop := make(chan struct{})
			var sampled sync.WaitGroup
			sampled.Go(func() {
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						var stats runtime.MemStats
						runtime.ReadMemStats(&stats)
						for previous := peak.Load(); stats.HeapAlloc > previous; previous = peak.Load() {
							if peak.CompareAndSwap(previous, stats.HeapAlloc) {
								break
							}
						}
					}
				}
			})
			sy := New(cfg, bucket, db, adapters.All(root), Options{})
			// Backdate: this fixture intentionally ends mid-line at the fixed byte boundary, and is
			// complete historical source content rather than a live partial-line deferral.
			quiet := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, quiet, quiet); err != nil {
				close(stop)
				sampled.Wait()
				t.Fatal(err)
			}
			sum, err := sy.Run(t.Context(), []adapters.SessionFile{{Agent: "codex", Root: root, Path: path, Key: "rollout.jsonl", Role: adapters.RoleAux, Zstd: compressed}})
			close(stop)
			sampled.Wait()
			if err != nil || sum.Errors != 0 {
				t.Fatalf("Run = %+v, %v", sum, err)
			}
			if peak.Load() > start.HeapAlloc+(48<<20) {
				t.Fatalf("capture heap growth = %d bytes for %d decoded bytes, want <=48 MiB", peak.Load()-start.HeapAlloc, length)
			}
			current, _, err := sy.current("file", "codex", "rollout.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			if current.File.Size != length || current.File.SHA256 != expected {
				t.Fatalf("capture identity = %d/%s, want %d/%s", current.File.Size, current.File.SHA256, length, expected)
			}
			if err := sy.Restore(t.Context(), "codex", "rollout.jsonl", "", io.Discard); err != nil {
				t.Fatal(err)
			}
			// The memory bucket retains only the small compressed dependency, never decoded content.
			for _, object := range bucket.objects {
				if bytes.Equal(object.body, input.pattern) {
					t.Fatal("unexpected plain transcript dependency")
				}
			}
		})
	}
}

func TestCompressedJSONLRetainsFinalDecodedFragment(t *testing.T) {
	// Fresh plaintext JSONL is held back, but a closed zstd artifact retains all bytes,
	// even when the last decoded line is partial or there is no complete first line.
	for _, body := range []string{"{\"done\":true}\n{\"partial\":", "{\"partial\":"} {
		t.Run(raw.SHA256([]byte(body))[:8], func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "rollout.jsonl.zst")
			if err := os.WriteFile(path, raw.Compress([]byte(body)), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
			db, err := state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			sy := New(cfg, newMemStore(), db, adapters.All(home), Options{})
			sf := adapters.SessionFile{Agent: "codex", Root: home, Path: path, Key: "rollout.jsonl", Role: adapters.RoleAux, Zstd: true}
			sum, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
			if err != nil || sum.Errors != 0 || sum.Uploaded != 1 {
				t.Fatalf("Run = %+v, %v, want complete compressed artifact", sum, err)
			}
			var restored bytes.Buffer
			if err := sy.Restore(t.Context(), sf.Agent, sf.Key, "", &restored); err != nil {
				t.Fatal(err)
			}
			if restored.String() != body {
				t.Fatalf("Restore = %q, want %q", restored.String(), body)
			}
		})
	}
}
