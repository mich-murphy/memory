package syncer

import (
	"context"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/testenv"
)

// memStore is a bucket in memory that records every request, so the golden test can compare
// what a run sends and the benchmarks can count it. latency, when set, is added to each request
// as a stand-in for R2's round trip. pin records the content of each raw object put, which the
// golden test checks and the benchmarks leave out, since it costs more than the put.
type memStore struct {
	mu      sync.Mutex
	objects map[string]memObject
	ops     []string
	sent    int64
	latency time.Duration
	pin     bool
}

type memObject struct {
	body []byte
	meta map[string]string
}

func newMemStore() *memStore {
	return &memStore{objects: map[string]memObject{}}
}

// record is called with mu held.
func (m *memStore) record(op string, sent int) {
	m.ops = append(m.ops, op)
	m.sent += int64(sent)
}

// wait stands in for a request's round trip; requests on different goroutines overlap.
func (m *memStore) wait() {
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
}

func (m *memStore) Put(_ context.Context, key string, body []byte, meta map[string]string) error {
	m.wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObject{slices.Clone(body), maps.Clone(meta)}
	op := "put " + key
	if m.pin && strings.HasSuffix(key, ".zst") {
		data, err := raw.Decompress(body)
		if err != nil {
			return err
		}
		op += " raw=" + raw.SHA256(data)
	}
	m.record(op, len(body))
	return nil
}

func (m *memStore) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return m.Put(ctx, key, body, meta)
}

func (m *memStore) Copy(_ context.Context, src, dst string) (bool, error) {
	m.wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[src]
	if ok {
		m.objects[dst] = o
	}
	m.record("copy "+src+" "+dst, 0)
	return ok, nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	m.record("delete "+key, 0)
	return nil
}

func (m *memStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objects[key].body, nil
}

// takeOps returns the requests since the last call, sorted, since uploads run concurrently.
func (m *memStore) takeOps() (ops []string, sent int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ops, sent = m.ops, m.sent
	m.ops, m.sent = nil, 0
	slices.Sort(ops)
	return ops, sent
}

// rig runs syncs against a workload the way the command line does, with a clock the caller
// sets.
type rig struct {
	tb     testing.TB
	w      *testenv.Workload
	cfg    *config.Config
	bucket *memStore
	now    time.Time
}

func newRig(tb testing.TB, w *testenv.Workload, stateDir string) *rig {
	tb.Helper()
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR"} {
		tb.Setenv(v, "")
	}
	return &rig{
		tb: tb, w: w, bucket: newMemStore(), now: w.Start,
		cfg: &config.Config{
			MachineID: "bench", Hostname: "bench", Home: w.Home, StateDir: stateDir,
			Path: filepath.Join(stateDir, "config.env"),
		},
	}
}

// sync runs one sync: a sweep of every agent when hookPaths is empty, else a hook's sync of
// those transcripts.
func (r *rig) sync(agent string, hookPaths ...string) *Summary {
	r.tb.Helper()
	return r.run(func(s *Syncer) ([]adapters.SessionFile, error) {
		if len(hookPaths) == 0 {
			return s.SweepFiles(context.Background(), adapters.Names)
		}
		s.opts.Trigger = "hook:test"
		return s.HookFiles(agent, hookPaths)
	})
}

// syncWith runs before, then a sync with no files, as a session-start hook does.
func (r *rig) syncWith(before func(*Syncer) error) *Summary {
	r.tb.Helper()
	return r.run(func(s *Syncer) ([]adapters.SessionFile, error) { return nil, before(s) })
}

func (r *rig) run(files func(*Syncer) ([]adapters.SessionFile, error)) *Summary {
	r.tb.Helper()
	ctx := context.Background()
	db, err := state.Open(ctx, r.cfg.StateDir)
	if err != nil {
		r.tb.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			r.tb.Fatal(err)
		}
	}()
	s := New(r.cfg, r.bucket, db, adapters.All(r.w.Home), Options{
		Trigger: "sweep:test", Logger: slog.New(slog.DiscardHandler), Version: "test",
	})
	s.clock = func() time.Time { return r.now }
	found, err := files(s)
	if err != nil {
		r.tb.Fatal(err)
	}
	sum, err := s.Run(ctx, found)
	if err != nil {
		r.tb.Fatal(err)
	}
	return sum
}

func copyDir(tb testing.TB, src, dst string) {
	tb.Helper()
	if err := os.RemoveAll(dst); err != nil {
		tb.Fatal(err)
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		tb.Fatal(err)
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		return os.Chtimes(filepath.Join(dst, rel), st.ModTime(), st.ModTime())
	})
	if err != nil {
		tb.Fatal(err)
	}
}

// resolvedTempDir resolves symlinks, so on macOS (where /var links to /private/var) snapshots
// don't record a symlink target for every file.
func resolvedTempDir(tb testing.TB) string {
	tb.Helper()
	dir, err := filepath.EvalSymlinks(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	return dir
}
