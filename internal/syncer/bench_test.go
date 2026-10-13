package syncer

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/testenv"
)

// The benchmarks run the collector the way it runs on a machine: a backfill once, then at every
// session start and end a sweep of every agent, and after every agent turn a hook's sync of
// that session. They use the in-memory bucket, so they measure the collector, not the network;
// the rtt variants add a fixed round trip per request to show what R2's latency costs.
// docs/benchmarks.md explains how to run and compare them.

var largeProfile = testenv.Profile{Sessions: 1, Largest: 72 << 20}

var profiles = []struct {
	name string
	p    testenv.Profile
}{
	{"small", testenv.SmallProfile},
	{"typical", testenv.TypicalProfile},
	{"large", largeProfile},
}

// benchHome is a workload and the state dir a backfill of it left, built once per test binary
// and copied by each benchmark, since a typical home takes seconds to write.
type benchHome struct {
	w      *testenv.Workload
	synced string
	sizes  map[string]int64
	at     time.Time
}

var (
	homesMu sync.Mutex
	homes   = map[string]*benchHome{}
	homeDir string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if homeDir != "" {
		_ = os.RemoveAll(homeDir) // temp files; the OS cleans up whatever is left
	}
	os.Exit(code)
}

func home(b *testing.B, name string, p testenv.Profile) *benchHome {
	b.Helper()
	homesMu.Lock()
	defer homesMu.Unlock()
	if h, ok := homes[name]; ok {
		return h
	}
	if homeDir == "" {
		dir, err := os.MkdirTemp("", "agent-sessions-bench-") //nolint:usetesting // shared by every benchmark in the binary; TestMain removes it
		if err != nil {
			b.Fatal(err)
		}
		if homeDir, err = filepath.EvalSymlinks(dir); err != nil {
			b.Fatal(err)
		}
	}
	dir := filepath.Join(homeDir, name)
	// A fixed start makes every run of the benchmarks see the same home.
	w := testenv.NewWorkload(b, dir, p, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	h := &benchHome{w: w, synced: filepath.Join(dir, "synced"), sizes: map[string]int64{}, at: w.Start.Add(2 * time.Hour)}
	r := newRig(b, w, h.synced)
	r.now = h.at
	if sum := r.sync(""); sum.Errors > 0 {
		b.Fatalf("backfill: %v", sum.ErrorMessages)
	}
	for _, p := range w.Mains {
		st, err := os.Stat(p)
		if err != nil {
			b.Fatal(err)
		}
		h.sizes[p] = st.Size()
	}
	homes[name] = h
	return h
}

// reset puts the home's transcripts and a copy of its synced state back as the backfill left
// them, and returns a rig on that copy a minute after the backfill, so every run of a benchmark
// sees the same ages and snapshot checks.
func (h *benchHome) reset(b *testing.B, stateDir string) *rig {
	b.Helper()
	for p, size := range h.sizes {
		if err := os.Truncate(p, size); err != nil {
			b.Fatal(err)
		}
	}
	h.w.Touch(b, h.w.Start)
	copyDir(b, h.synced, stateDir)
	r := newRig(b, h.w, stateDir)
	r.now = h.at.Add(time.Minute)
	return r
}

// tally adds up requests and bytes sent over a benchmark's runs.
type tally struct{ requests, sent int64 }

func (t *tally) add(m *memStore) {
	ops, sent := m.takeOps()
	t.requests += int64(len(ops))
	t.sent += sent
}

func (t *tally) report(b *testing.B) {
	b.ReportMetric(float64(t.requests)/float64(b.N), "requests/op")
	b.ReportMetric(float64(t.sent)/float64(b.N), "sent-B/op")
}

func BenchmarkBackfill(b *testing.B) {
	for _, pr := range profiles {
		b.Run(pr.name, func(b *testing.B) {
			h := home(b, pr.name, pr.p)
			stateDir := filepath.Join(b.TempDir(), "state")
			b.SetBytes(h.w.Bytes)
			r := newRig(b, h.w, stateDir)
			r.now = h.at
			var t tally
			for b.Loop() {
				b.StopTimer()
				if err := os.RemoveAll(stateDir); err != nil {
					b.Fatal(err)
				}
				r.store.objects = map[string]memObject{}
				b.StartTimer()
				r.sync("")
				t.add(r.store)
			}
			t.report(b)
		})
	}
}

func BenchmarkSweepIdle(b *testing.B) {
	for _, pr := range profiles[:2] {
		b.Run(pr.name, func(b *testing.B) {
			r := home(b, pr.name, pr.p).reset(b, filepath.Join(b.TempDir(), "state"))
			var t tally
			for b.Loop() {
				r.sync("")
				t.add(r.store)
			}
			t.report(b)
		})
	}
}

// turnBytes is about what one agent turn adds to a transcript: a prompt, a few tool calls and
// their results, and a reply.
const turnBytes = 24 << 10

func BenchmarkSweepAfterTurns(b *testing.B) {
	for _, rtt := range []time.Duration{0, 30 * time.Millisecond} {
		b.Run(fmt.Sprintf("typical/rtt=%v", rtt), func(b *testing.B) {
			h := home(b, "typical", testenv.TypicalProfile)
			stateDir := filepath.Join(b.TempDir(), "state")
			n := testenv.TypicalProfile.Sessions
			active := []string{h.w.Mains[1], h.w.Mains[n+1], h.w.Mains[2*n+1]}
			var t tally
			for b.Loop() {
				b.StopTimer()
				r := h.reset(b, stateDir)
				r.store.latency = rtt
				for _, p := range active {
					h.w.AppendTurn(b, p, turnBytes, r.now.Add(-2*time.Minute))
				}
				b.StartTimer()
				r.sync("")
				t.add(r.store)
			}
			t.report(b)
		})
	}
}

// BenchmarkHookTurn is the sync after one turn of the largest Claude session in the home,
// which a hook starts after every turn.
func BenchmarkHookTurn(b *testing.B) {
	cases := []struct {
		name string
		p    testenv.Profile
		rtt  time.Duration
	}{
		{"typical", testenv.TypicalProfile, 0},
		{"typical", testenv.TypicalProfile, 30 * time.Millisecond},
		{"large", largeProfile, 0},
	}
	for _, c := range cases {
		b.Run(fmt.Sprintf("%s/rtt=%v", c.name, c.rtt), func(b *testing.B) {
			h := home(b, c.name, c.p)
			stateDir := filepath.Join(b.TempDir(), "state")
			path := h.w.Mains[0]
			var t tally
			for b.Loop() {
				b.StopTimer()
				r := h.reset(b, stateDir)
				r.store.latency = c.rtt
				h.w.AppendTurn(b, path, turnBytes, r.now.Add(-5*time.Second))
				b.StartTimer()
				r.sync("claude", path)
				t.add(r.store)
			}
			t.report(b)
		})
	}
}

// BenchmarkFold is the sweep that folds a session's segments into one object once it has been
// idle, which every session gets once after it ends.
func BenchmarkFold(b *testing.B) {
	h := home(b, "typical", testenv.TypicalProfile)
	stateDir := filepath.Join(b.TempDir(), "state")
	path := h.w.Mains[0]
	var t tally
	for b.Loop() {
		b.StopTimer()
		r := h.reset(b, stateDir)
		h.w.AppendTurn(b, path, turnBytes, r.now.Add(-5*time.Second))
		r.sync("claude", path)
		r.store.takeOps()
		r.now = r.now.Add(time.Hour)
		b.StartTimer()
		r.sync("claude", path)
		t.add(r.store)
	}
	t.report(b)
}
