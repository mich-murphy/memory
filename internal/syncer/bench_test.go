package syncer

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
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

type profile struct {
	name string
	p    testenv.Profile
}

// The profiles span the homes and sessions the collector meets: from 9 sessions to 1,200, and
// from a 160 KiB main transcript to a 256 MiB one.
var (
	small   = profile{"small", testenv.SmallProfile}
	typical = profile{"typical", testenv.TypicalProfile}
	many    = profile{"many", testenv.Profile{Sessions: 400, Median: 24 << 10, Largest: 1 << 20}}
	large   = profile{"large", testenv.Profile{Sessions: 1, Largest: 72 << 20}}
	huge    = profile{"huge", testenv.Profile{Sessions: 1, Largest: 256 << 20}}
)

// benchHome is a workload and the state dir a backfill of it left, built once per test binary
// and copied by each benchmark, since a typical home takes seconds to write.
type benchHome struct {
	w       *testenv.Workload
	synced  string
	sizes   map[string]int64
	at      time.Time
	objects map[string]memObject
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

func home(b *testing.B, pr profile) *benchHome {
	b.Helper()
	homesMu.Lock()
	defer homesMu.Unlock()
	if h, ok := homes[pr.name]; ok {
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
	dir := filepath.Join(homeDir, pr.name)
	// A fixed start makes every run of the benchmarks see the same home.
	w := testenv.NewWorkload(b, dir, pr.p, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	h := &benchHome{w: w, synced: filepath.Join(dir, "synced"), sizes: map[string]int64{}, at: w.Start.Add(2 * time.Hour)}
	r := newRig(b, w, h.synced)
	r.now = h.at
	benchSync(b, r, "")
	h.objects = maps.Clone(r.bucket.objects)
	for _, p := range w.Mains {
		st, err := os.Stat(p)
		if err != nil {
			b.Fatal(err)
		}
		h.sizes[p] = st.Size()
	}
	homes[pr.name] = h
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
	// The bound cache and its matching remote archive are one fixture snapshot. Bodies
	// are immutable; only the map needs copying before a benchmark adds publications.
	r.bucket.objects = maps.Clone(h.objects)
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

func benchSync(b *testing.B, r *rig, agent string, paths ...string) *Summary {
	b.Helper()
	sum := r.sync(agent, paths...)
	if sum.Errors != 0 {
		b.Fatalf("sync: %v", sum.ErrorMessages)
	}
	return sum
}

func BenchmarkBackfill(b *testing.B) {
	for _, pr := range []profile{small, typical, many, large, huge} {
		b.Run(pr.name, func(b *testing.B) {
			h := home(b, pr)
			stateDir := filepath.Join(b.TempDir(), "state")
			b.SetBytes(h.w.Size)
			r := newRig(b, h.w, stateDir)
			r.now = h.at
			var t tally
			for b.Loop() {
				b.StopTimer()
				if err := os.RemoveAll(stateDir); err != nil {
					b.Fatal(err)
				}
				r.bucket.objects = map[string]memObject{}
				b.StartTimer()
				if sum := benchSync(b, r, ""); sum.Uploaded == 0 || sum.Manifests == 0 {
					b.Fatalf("backfill did not capture workload: %+v", sum)
				}
				t.add(r.bucket)
			}
			t.report(b)
		})
	}
}

func BenchmarkSweepIdle(b *testing.B) {
	for _, pr := range []profile{small, typical, many} {
		b.Run(pr.name, func(b *testing.B) {
			r := home(b, pr).reset(b, filepath.Join(b.TempDir(), "state"))
			var t tally
			for b.Loop() {
				if sum := benchSync(b, r, ""); sum.Uploaded != 0 {
					b.Fatalf("idle sweep uploaded %d files", sum.Uploaded)
				}
				t.add(r.bucket)
			}
			t.report(b)
		})
	}
}

// turnSize is about what one agent turn adds to a transcript: a prompt, a few tool calls and
// their results, and a reply.
const turnSize = 24 << 10

func BenchmarkSweepAfterTurns(b *testing.B) {
	for _, rtt := range []time.Duration{0, 30 * time.Millisecond} {
		b.Run(fmt.Sprintf("typical/rtt=%v", rtt), func(b *testing.B) {
			h := home(b, typical)
			stateDir := filepath.Join(b.TempDir(), "state")
			n := typical.p.Sessions
			active := []string{h.w.Mains[1], h.w.Mains[n+1], h.w.Mains[2*n+1]}
			var t tally
			for b.Loop() {
				b.StopTimer()
				r := h.reset(b, stateDir)
				r.bucket.latency = rtt
				for _, p := range active {
					h.w.AppendTurn(b, p, turnSize, r.now.Add(-2*time.Minute))
				}
				b.StartTimer()
				if sum := benchSync(b, r, ""); sum.Segments != len(active) {
					b.Fatalf("busy sweep captured %d segments, want %d", sum.Segments, len(active))
				}
				t.add(r.bucket)
			}
			t.report(b)
		})
	}
}

// BenchmarkHookTurn is the sync after one turn of the largest Claude session in the home,
// which a hook starts after every turn.
func BenchmarkHookTurn(b *testing.B) {
	cases := []struct {
		profile
		rtt time.Duration
	}{
		{small, 0}, {typical, 0}, {typical, 30 * time.Millisecond}, {many, 0}, {large, 0}, {huge, 0},
	}
	for _, c := range cases {
		b.Run(fmt.Sprintf("%s/rtt=%v", c.name, c.rtt), func(b *testing.B) {
			h := home(b, c.profile)
			stateDir := filepath.Join(b.TempDir(), "state")
			path := h.w.Mains[0]
			var t tally
			for b.Loop() {
				b.StopTimer()
				r := h.reset(b, stateDir)
				r.bucket.latency = c.rtt
				h.w.AppendTurn(b, path, turnSize, r.now.Add(-5*time.Second))
				b.StartTimer()
				if sum := benchSync(b, r, "claude", path); sum.Segments != 1 {
					b.Fatalf("hook turn captured %d segments, want 1", sum.Segments)
				}
				t.add(r.bucket)
			}
			t.report(b)
		})
	}
}

// segmented is a 12 MiB Claude session after each of a hook sync per turn for some number of
// turns: the transcript at its longest, and per number of turns, its size, the state and the
// clock after that turn.
type segmented struct {
	h       *benchHome
	full    []byte
	sizes   map[int]int64
	states  map[int]string
	at      map[int]time.Time
	objects map[int]map[string]memObject
}

var (
	segmentedOnce sync.Once
	segmentedHome *segmented
)

func segmentedSession(b *testing.B, turns []int) *segmented {
	b.Helper()
	segmentedOnce.Do(func() {
		h := home(b, profile{"segmented", testenv.Profile{Sessions: 1, Largest: 12 << 20}})
		s := &segmented{h: h, sizes: map[int]int64{}, states: map[int]string{}, at: map[int]time.Time{}, objects: map[int]map[string]memObject{}}
		dir := filepath.Join(filepath.Dir(h.synced), "turns")
		r := h.reset(b, filepath.Join(dir, "work"))
		path := h.w.Mains[0]
		done := 0
		for _, k := range turns {
			for ; done < k; done++ {
				r.now = r.now.Add(10 * time.Second)
				h.w.AppendTurn(b, path, turnSize, r.now.Add(-5*time.Second))
				if sum := benchSync(b, r, "claude", path); sum.Segments != 1 {
					b.Fatalf("turn %d captured %d segments, want 1", done, sum.Segments)
				}
			}
			st, err := os.Stat(path)
			if err != nil {
				b.Fatal(err)
			}
			s.states[k] = filepath.Join(dir, strconv.Itoa(k))
			copyDir(b, filepath.Join(dir, "work"), s.states[k])
			s.sizes[k], s.at[k] = st.Size(), r.now
			s.objects[k] = maps.Clone(r.bucket.objects)
		}
		full, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		s.full = full
		segmentedHome = s
	})
	if segmentedHome == nil {
		b.Fatal("building the segmented session failed in an earlier benchmark")
	}
	return segmentedHome
}

// BenchmarkHookTurnAfterSegments is the hook sync of a session that has gathered segments since
// its last fold, up to the most a session gathers before it is folded.
func BenchmarkHookTurnAfterSegments(b *testing.B) {
	turns := []int{1, 16, maxSegments - 2}
	for _, k := range turns {
		b.Run(fmt.Sprintf("segments=%d", k), func(b *testing.B) {
			s := segmentedSession(b, turns)
			path := s.h.w.Mains[0]
			stateDir := filepath.Join(b.TempDir(), "state")
			var t tally
			for b.Loop() {
				b.StopTimer()
				// Written whole rather than truncated, since growing a file by truncation pads
				// it with zeros.
				if err := os.WriteFile(path, s.full[:s.sizes[k]], 0o600); err != nil {
					b.Fatal(err)
				}
				copyDir(b, s.states[k], stateDir)
				r := newRig(b, s.h.w, stateDir)
				r.bucket.objects = maps.Clone(s.objects[k])
				r.now = s.at[k].Add(10 * time.Second)
				s.h.w.AppendTurn(b, path, turnSize, r.now.Add(-5*time.Second))
				b.StartTimer()
				if sum := benchSync(b, r, "claude", path); sum.Segments != 1 {
					b.Fatalf("segmented hook captured %d segments, want 1", sum.Segments)
				}
				t.add(r.bucket)
			}
			t.report(b)
		})
	}
}

// BenchmarkFold is the sweep that folds a session's segments into one object once it has been
// idle, which every session gets once after it ends.
func BenchmarkFold(b *testing.B) {
	h := home(b, typical)
	stateDir := filepath.Join(b.TempDir(), "state")
	path := h.w.Mains[0]
	var t tally
	for b.Loop() {
		b.StopTimer()
		r := h.reset(b, stateDir)
		h.w.AppendTurn(b, path, turnSize, r.now.Add(-5*time.Second))
		if sum := benchSync(b, r, "claude", path); sum.Segments != 1 {
			b.Fatalf("fold setup captured %d segments, want 1", sum.Segments)
		}
		r.bucket.takeOps()
		r.now = r.now.Add(time.Hour)
		b.StartTimer()
		if sum := benchSync(b, r, "claude", path); sum.Folded != 1 {
			b.Fatalf("idle sweep folded %d files, want 1", sum.Folded)
		}
		t.add(r.bucket)
	}
	t.report(b)
}
