package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturedaemon"
)

const benchFilesPerRoot = 1400

// BenchmarkDiscoveryCall measures one full discovery call (real snapshotPolicy,
// scopePolicy and Discover) over three roots of benchFilesPerRoot synthetic files.
func BenchmarkDiscoveryCall(b *testing.B) {
	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	c := capturedaemon.Config{Version: capturedaemon.ConfigVersion, ArchiveID: "runtime-test", MachineID: "test-machine", Store: filepath.Join(root, "store"), Spool: filepath.Join(root, "spool"), PolicyDir: filepath.Join(root, "policy"), Home: filepath.Join(root, "home"), DefaultRoots: true, CaptureWorkers: 1, DeliveryWorkers: 1, PollSeconds: 1, OperationSeconds: 1, MaxAttempts: 2, MaxSpoolBytes: 1 << 20, EntriesPerRoot: 4096, QuietSeconds: new(0), MaxDelaySeconds: new(0)}
	if err := os.MkdirAll(filepath.Join(c.PolicyDir, "exclusions"), 0o700); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(c.Spool, 0o700); err != nil {
		b.Fatal(err)
	}
	config := filepath.Join(c.Home, "capture.json")
	if err := capturedaemon.Save(config, c); err != nil {
		b.Fatal(err)
	}
	for i := range benchFilesPerRoot {
		uuid := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
		for _, p := range []string{
			filepath.Join(c.Home, ".claude", "projects", "p", uuid+".jsonl"),
			filepath.Join(c.Home, ".codex", "sessions", "2026", "01", "01", "rollout-2026-01-01T00-00-00-"+uuid+".jsonl"),
			filepath.Join(c.Home, ".pi", "agent", "sessions", "--p--", "2026-01-01T00-00-00-000Z_"+uuid+".jsonl"),
		} {
			mustWrite(b, p)
		}
	}
	r, err := newRuntimeComposition(c)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		discover, closeCursor := r.discovery(config)
		batch, err := discover(b.Context())
		if err != nil {
			b.Fatal(err)
		}
		if err := closeCursor(); err != nil {
			b.Fatal(err)
		}
		if len(batch.Requests) < 2*benchFilesPerRoot {
			b.Fatalf("requests = %d, fixture not recognised", len(batch.Requests))
		}
	}
}
