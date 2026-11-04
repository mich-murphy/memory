package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// TestDiscoveryInstalledScale logs cold, settled and changed sweep cost over an
// installed-scale inventory with the real runtime policy. It is a measurement, not
// an assertion: set DT_SCALE=<sources> (and optionally DT_EXCLUSIONS=<records>).
func TestDiscoveryInstalledScale(t *testing.T) {
	total, _ := strconv.Atoi(os.Getenv("DT_SCALE"))
	if total == 0 {
		t.Skip("set DT_SCALE=<sources>")
	}
	exclusions, _ := strconv.Atoi(os.Getenv("DT_EXCLUSIONS"))
	c, config := runtimeFixture(t)
	c.EntriesPerRoot, c.MaxInventoryRecords, c.OperationSeconds = 4096, 32768, 60
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(filepath.Dir(c.Home), "real")
	paths := writeScaleFixture(t, c.Home, realDir, total)
	for i := range exclusions {
		sf := adapters.SessionFile{Agent: "claude", Root: filepath.Join(c.Home, ".claude"), Path: filepath.Join(realDir, "gone", strconv.Itoa(i)+".jsonl"), SessionKey: "gone/" + strconv.Itoa(i)}
		rec, err := exclusion.RecordFile(c.PolicyDir, sf)
		if err != nil {
			t.Fatal(err)
		}
		_ = rec
	}
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
	defer func() { _ = closeCursor() }()
	sweep := func(name string) {
		var calls, reqs int
		var walk, admit time.Duration
		for done := false; !done; calls++ {
			t0 := time.Now()
			batch, err := discover(t.Context())
			walk += time.Since(t0)
			if err != nil {
				t.Fatal(err)
			}
			t1 := time.Now()
			if err := q.HintBatch(t.Context(), batch.Requests); err != nil {
				t.Fatal(err)
			}
			admit += time.Since(t1)
			reqs += len(batch.Requests)
			done = batch.Complete
		}
		t.Logf("%s: sources=%d exclusions=%d calls=%d requests=%d walk=%v admit=%v maxPerCall=%v", name, total, exclusions, calls, reqs, walk, admit, (walk+admit)/time.Duration(calls))
	}
	sweep("cold")
	sweep("settled")
	for i := 0; i < len(paths); i += 10 {
		if err := os.WriteFile(paths[i], []byte("changed changed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sweep("changed10pct")
}

// writeScaleFixture lays out claude (symlinked root), codex and pi sources in the
// proportions measured on the installed hosts and returns every regular source path.
func writeScaleFixture(t *testing.T, home, realDir string, total int) []string {
	t.Helper()
	var out []string
	write := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("synthetic\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(realDir, "claude"), filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	nc, np := total/2, total/3
	for i := range nc {
		sid := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
		proj := filepath.Join(realDir, "claude", "projects", fmt.Sprintf("p%d", i%200))
		if i%3 == 0 {
			write(filepath.Join(proj, sid+".jsonl"))
		} else {
			write(filepath.Join(proj, sid, "subagents", "agent-a.jsonl"))
		}
	}
	for i := range np {
		write(filepath.Join(home, ".pi", "agent", "sessions", fmt.Sprintf("--d%d--", i%100), fmt.Sprintf("2026-01-01T00-00-00-%03d_%08x-0000-4000-8000-%012x.jsonl", i%1000, i, i)))
	}
	for i := range total - nc - np {
		write(filepath.Join(home, ".codex", "sessions", "2026", fmt.Sprintf("%02d", i%12+1), fmt.Sprintf("%02d", i%28+1), fmt.Sprintf("rollout-2026-01-01T00-00-00-%08x-0000-4000-8000-%012x.jsonl", i, i)))
	}
	return out
}
