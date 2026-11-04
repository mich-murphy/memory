package cli

import (
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturedaemon"
)

const benchOwners = 50

// BenchmarkDiscoveryCallExternal measures one discovery call over Claude external
// artifacts (externalPerOwner each for benchOwners owners recorded in a real queue).
func BenchmarkDiscoveryCallExternal(b *testing.B) {
	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	c := capturedaemon.Config{Version: capturedaemon.ConfigVersion, ArchiveID: "runtime-test", MachineID: "test-machine", Store: filepath.Join(root, "store"), Spool: filepath.Join(root, "spool"), PolicyDir: filepath.Join(root, "policy"), Home: filepath.Join(root, "home"), DefaultRoots: true, CaptureWorkers: 1, DeliveryWorkers: 1, PollSeconds: 1, OperationSeconds: 1, MaxAttempts: 2, MaxSpoolBytes: 1 << 20, EntriesPerRoot: 4096, QuietSeconds: new(0), MaxDelaySeconds: new(0)}
	mustMkdir(b, filepath.Join(c.PolicyDir, "exclusions"))
	config := filepath.Join(c.Home, "capture.json")
	if err := capturedaemon.Save(config, c); err != nil {
		b.Fatal(err)
	}
	mains := writeOwnerTree(b, c, benchOwners)
	r, err := newRuntimeComposition(c)
	if err != nil {
		b.Fatal(err)
	}
	recordOwners(b, r, config, mains...)
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
		if len(batch.Requests) < benchOwners*(externalPerOwner+1) {
			b.Fatalf("requests = %d, fixture not recognised", len(batch.Requests))
		}
	}
}
