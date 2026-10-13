package syncer

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/testenv"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.txt from this run")

// TestBucketMatchesGolden runs a session's life on a fixed workload and compares every request
// and every byte the bucket ends up with, compressed and not, against testdata/golden.txt. A
// change that alters what is captured or sent shows up here as a diff; rewrite the file with
// -update only when the change is meant to.
func TestBucketMatchesGolden(t *testing.T) {
	oldLarge, oldChunk := largeFileBytes, chunkSize
	largeFileBytes, chunkSize = 100<<10, 4<<10 // the largest transcripts take the streamed path
	t.Cleanup(func() { largeFileBytes, chunkSize = oldLarge, oldChunk })

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	dir := resolvedTempDir(t)
	w := testenv.NewWorkload(t, dir, testenv.SmallProfile, t0.Add(-2*time.Hour))
	r := newRig(t, w, filepath.Join(dir, "state"))
	n := testenv.SmallProfile.Sessions
	claude, codex, pi := w.Mains[0], w.Mains[n], w.Mains[2*n]

	var out []section
	step := func(name string, at time.Time, sync func() *Summary) {
		r.now = at
		sum := sync()
		ops, _ := r.store.takeOps()
		body, err := jsonx.Compact(sum.Trimmed())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, section{fmt.Sprintf("== %s\nsummary %s", name, body), ops})
	}
	sweep := func() *Summary { return r.sync("") }

	step("backfill", t0, sweep)
	step("idle sweep", t0.Add(time.Minute), sweep)

	for _, p := range []string{claude, codex, pi} {
		w.AppendTurn(t, p, 6<<10, t0.Add(4*time.Minute))
	}
	testenv.Append(t, w.Mains[1], `{"type":"user","message":{"role":"user","content":"half`)
	if err := os.Chtimes(w.Mains[1], t0.Add(6*time.Minute), t0.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	step("sweep after turns", t0.Add(6*time.Minute), sweep)

	w.AppendTurn(t, claude, 3<<10, t0.Add(8*time.Minute))
	step("hook after a turn", t0.Add(9*time.Minute), func() *Summary { return r.sync("claude", claude) })

	launch := map[string]any{
		"event": "SessionStart", "source": "resume", "cwd": "/home/dev/code/proj-0", "recorded_at": 1.5e9,
		"hook_input": map[string]any{"session_id": "x", "transcript_path": codex},
	}
	step("launch", t0.Add(10*time.Minute), func() *Summary {
		return r.syncWith(func(s *Syncer) error { return s.RecordLaunch(t.Context(), "codex", codex, launch) })
	})

	rewrite(t, w.Mains[2*n+1], t0.Add(10*time.Minute))
	removeTodo(t, w)
	step("rewrite and delete", t0.Add(11*time.Minute), sweep)

	step("fold once idle", t0.Add(40*time.Minute), sweep)

	out = append(out, section{"== bucket", bucketDigest(t, r.store)})
	got := render(out, w, dir, setIDs(r.store, w, dir))

	golden := filepath.Join("testdata", "golden.txt")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("bucket differs from %s (-want +got):\n%s", golden, diff)
	}
}

func rewrite(t *testing.T, path string, at time.Time) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"version":3`), []byte(`"version":4`), 1)
	testenv.Write(t, path, string(data))
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func removeTodo(t *testing.T, w *testenv.Workload) {
	t.Helper()
	todos, err := filepath.Glob(filepath.Join(w.Home, ".claude", "todos", "*.json"))
	if err != nil || len(todos) == 0 {
		t.Fatalf("no todos: %v", err)
	}
	if err := os.Remove(todos[0]); err != nil {
		t.Fatal(err)
	}
}

// normalise drops the temp directory, which differs per run, from paths in the output.
func normalise(b []byte, w *testenv.Workload, dir string) string {
	return strings.NewReplacer(w.Home, "$HOME", dir, "$DIR").Replace(string(b))
}

// setIDs replaces snapshot set ids, which hash a set that holds the temp directory's path, by
// the set's place among the sets sorted by their normalised bodies.
func setIDs(m *memStore, w *testenv.Workload, dir string) *strings.Replacer {
	type set struct{ id, body string }
	var sets []set
	for k, o := range m.objects {
		if id, ok := strings.CutPrefix(k, "snapshots/sets/"); ok {
			sets = append(sets, set{strings.TrimSuffix(id, ".json"), normalise(o.body, w, dir)})
		}
	}
	slices.SortFunc(sets, func(a, b set) int { return strings.Compare(a.body, b.body) })
	var pairs []string
	for i, s := range sets {
		pairs = append(pairs, s.id, fmt.Sprintf("set-%d", i))
	}
	return strings.NewReplacer(pairs...)
}

// section is a heading and lines that are sorted once set ids are replaced, since uploads run
// concurrently.
type section struct {
	head  string
	lines []string
}

func render(sections []section, w *testenv.Workload, dir string, ids *strings.Replacer) string {
	var b strings.Builder
	for _, s := range sections {
		lines := make([]string, len(s.lines))
		for i, l := range s.lines {
			lines[i] = ids.Replace(normalise([]byte(l), w, dir))
		}
		slices.Sort(lines)
		b.WriteString(s.head + "\n")
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// bucketDigest lists every object with its metadata and the sha256 of its stored and its
// uncompressed bytes. Manifests, snapshot sets and version lists hold the temp directory's
// path, so they are shown whole instead, to be normalised.
func bucketDigest(t *testing.T, m *memStore) []string {
	t.Helper()
	var out []string
	for k, o := range m.objects {
		if strings.HasPrefix(k, "manifests/") || strings.HasPrefix(k, "snapshots/sets/") || strings.HasPrefix(k, "raw-versions/") {
			out = append(out, k+"\n"+string(o.body))
			continue
		}
		meta, err := jsonx.Compact(o.meta)
		if err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("%s %s size=%d stored=%s", k, meta, len(o.body), raw.SHA256(o.body))
		if strings.HasSuffix(k, ".zst") {
			data, err := raw.Decompress(o.body)
			if err != nil {
				t.Fatal(err)
			}
			line += fmt.Sprintf(" raw=%s rawsize=%d", raw.SHA256(data), len(data))
		}
		out = append(out, line)
	}
	return out
}
