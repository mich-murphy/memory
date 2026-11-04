package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

const snapshotFiles = 20

func snapshotFixture(t *testing.T) (capturedaemon.Config, string, []string) {
	t.Helper()
	c, config := runtimeFixture(t)
	c.EntriesPerRoot = 256
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(c.Home, ".claude", "projects", "p")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := range snapshotFiles {
		p := filepath.Join(dir, fmt.Sprintf("%08x-0000-4000-8000-%012x.jsonl", i, i))
		if err := os.WriteFile(p, []byte("synthetic\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return c, config, paths
}

func countingComposition(t *testing.T, c capturedaemon.Config) (runtimeComposition, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	var loads, sets atomic.Int64
	r.registryLoader = func(dir string) (exclusion.Registry, error) {
		loads.Add(1)
		return exclusion.Load(dir)
	}
	r.filesystemSet = func(c capturedaemon.Config) (*capturepolicy.Policy, error) {
		sets.Add(1)
		return newRuntimeFilesystemSet(c)
	}
	return r, &loads, &sets
}

// The cost of a discovery call must not scale with the number of entries it walks:
// one registry load and one filesystem policy set per call, however many entries.
func TestDiscoveryCallBuildsPolicyOncePerCall(t *testing.T) {
	c, config, _ := snapshotFixture(t)
	r, loads, sets := countingComposition(t, c)
	discover, closeCursor := r.discovery(config)
	defer func() { _ = closeCursor() }()
	for call := int64(1); call <= 2; call++ {
		batch, err := discover(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(batch.Requests) != snapshotFiles || !batch.Complete {
			t.Fatalf("call %d: requests=%d complete=%v, want %d complete", call, len(batch.Requests), batch.Complete, snapshotFiles)
		}
		if loads.Load() != call || sets.Load() != call {
			t.Fatalf("after call %d: registry loads=%d filesystem sets=%d, want %d of each (one fresh snapshot per call)", call, loads.Load(), sets.Load(), call)
		}
	}
}

// The live policy used for capture is never snapshotted: every check re-reads state.
func TestCapturePolicyIsNotSnapshotted(t *testing.T) {
	c, _, paths := snapshotFixture(t)
	r, loads, sets := countingComposition(t, c)
	policy := r.policy(false)
	req := sessioncapture.Request{Path: paths[0]}
	req.Identity.ArchiveID, req.Identity.MachineID, req.Identity.Agent = c.ArchiveID, c.MachineID, "claude"
	for range 3 {
		_ = policy(t.Context(), req)
	}
	if loads.Load() != 3 || sets.Load() != 3 {
		t.Fatalf("live policy loads=%d sets=%d, want 3 and 3", loads.Load(), sets.Load())
	}
}

// An exclusion saved after a call took its snapshot cannot be missed: the request the
// stale snapshot admitted is refused at capture, and the next call omits the source.
func TestExclusionAfterSnapshotStillPreventsCapture(t *testing.T) {
	c, config, paths := snapshotFixture(t)
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	q, err := sessioncapture.Open(t.Context(), c.Spool, r.queueOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close() }()
	discover, closeCursor := r.discovery(config)
	batch, err := discover(t.Context())
	_ = closeCursor()
	if err != nil || len(batch.Requests) != snapshotFiles {
		t.Fatalf("first call requests=%d err=%v", len(batch.Requests), err)
	}
	// The exclusion arrives after the snapshot was taken and the requests returned.
	target := paths[3]
	sf, ok := adapters.All(c.Home)["claude"].Classify(target, filepath.Join(c.Home, ".claude"))
	if !ok || sf.SessionKey == "" {
		t.Fatal("fixture path has no session identity")
	}
	if _, err := exclusion.RecordFile(c.PolicyDir, sf); err != nil {
		t.Fatal(err)
	}
	for _, req := range batch.Requests {
		if req.Path != target {
			continue
		}
		if _, err := q.Capture(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
			t.Fatalf("stale-snapshot request captured or wrong error: %v", err)
		}
		discover, closeCursor = r.discovery(config)
		next, err := discover(t.Context())
		_ = closeCursor()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range next.Requests {
			if n.Path == target {
				t.Fatal("next call's fresh snapshot still admitted the excluded source")
			}
		}
		if len(next.Requests) != snapshotFiles-1 {
			t.Fatalf("next call requests=%d, want %d", len(next.Requests), snapshotFiles-1)
		}
		return
	}
	t.Fatal("target source was not discovered")
}
