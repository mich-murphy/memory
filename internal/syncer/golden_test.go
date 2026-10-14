package syncer

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/jsonx"
	launchpkg "github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/testenv"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.txt from this run")

// TestBucketMatchesGolden runs a session's life on a fixed workload and compares every request
// and every byte the bucket ends up with, compressed and not, against testdata/golden.txt. A
// change that alters what is captured or sent shows up here as a diff; rewrite the file with
// -update only when the change is meant to.
func TestBucketMatchesGolden(t *testing.T) {
	oldLarge, oldChunk := largeFileSize, chunkSize
	largeFileSize, chunkSize = 100<<10, 4<<10 // the largest transcripts take the streamed path
	t.Cleanup(func() { largeFileSize, chunkSize = oldLarge, oldChunk })

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	dir := resolvedTempDir(t)
	w := testenv.NewWorkload(t, dir, testenv.SmallProfile, t0.Add(-2*time.Hour))
	r := newRig(t, w, filepath.Join(dir, "state"))
	r.bucket.pin = true
	n := testenv.SmallProfile.Sessions
	claude, codex, pi := w.Mains[0], w.Mains[n], w.Mains[2*n]

	var out []section
	step := func(name string, at time.Time, sync func() *Summary) {
		r.now = at
		sum := sync()
		if sum.Errors != 0 {
			t.Fatalf("%s: %v", name, sum.ErrorMessages)
		}
		if err := testenv.Audit(slices.Collect(maps.Keys(r.bucket.objects)), memRead(r.bucket)); err != nil {
			t.Fatalf("%s independent archive audit: %v", name, err)
		}
		ops, _ := r.bucket.takeOps()
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
		return r.syncWith(func(s *Syncer) error {
			return s.RecordEnvelope(t.Context(), launchpkg.Envelope{Schema: 1, ID: "golden-launch", Agent: "codex", Path: codex, RecordedAt: 1.5e9, Payload: launch})
		})
	})

	rewrite(t, w.Mains[2*n+1], t0.Add(10*time.Minute))
	removeTodo(t, w)
	step("rewrite and delete", t0.Add(11*time.Minute), sweep)

	step("fold once idle", t0.Add(40*time.Minute), sweep)

	ids, bodies := archiveIDs(t, r.bucket, w, dir)
	out = append(out, section{"== bucket", bucketDigest(t, r.bucket, bodies)})
	got := render(out, w, dir, ids)

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

// archiveIDs names only path-derived hashes and nondeterministic coordination IDs.
// Descriptor bodies remain fully visible. The physical originals were independently
// audited before normalization, including their true byte sizes and SHA-256 identities.
func archiveIDs(t *testing.T, m *memStore, w *testenv.Workload, dir string) (*strings.Replacer, map[string]string) {
	t.Helper()
	type item struct {
		key, id, sort string
		rank          int
	}
	var items []item
	var pairs []string
	for key, o := range m.objects {
		if strings.Contains(key, "/records/") && strings.Contains(key, "/commits/") {
			r, err := testenv.DecodeRecord(key, o.body)
			if err != nil {
				t.Fatal(err)
			}
			rank := 0
			if r.Kind == "launch" {
				rank = 1
			}
			if r.Kind == "manifest" {
				rank = 2
			}
			items = append(items, item{key, r.ID, fmt.Sprintf("%s/%s/%s/%020d", r.Kind, r.Agent, normalise([]byte(r.Key), w, dir), r.Sequence), rank})
			if r.File != nil {
				root := r.File.Identity.Root
				pairs = append(pairs, raw.SHA256([]byte(root))[:10], raw.SHA256([]byte(normalise([]byte(root), w, dir)))[:10])
			}
			oldPrefix := testenv.Stream(r.MachineID, r.Kind, r.Agent, r.Key)
			normalizedKey := normalise([]byte(r.Key), w, dir)
			if r.Root != nil {
				path := r.Root["path"].(string)
				normalizedKey = raw.SHA256([]byte(r.Agent + "\x00" + normalise([]byte(path), w, dir)))
				pairs = append(pairs, r.Key, normalizedKey)
			}
			newPrefix := testenv.Stream(r.MachineID, r.Kind, r.Agent, normalizedKey)
			if oldPrefix != newPrefix {
				pairs = append(pairs, oldPrefix, newPrefix)
			}
		}
		if strings.HasPrefix(key, "v3/snapshots/sets/") {
			items = append(items, item{key, raw.SHA256(o.body), "set/" + normalise(o.body, w, dir), 0})
		}
	}
	slices.SortFunc(items, func(a, b item) int { return strings.Compare(a.sort, b.sort) })
	for i, item := range items {
		pairs = append(pairs, item.id, fmt.Sprintf("$descriptor-%03d", i))
	}
	// Store UUID and machine checkpoint token are coordination identities, not election rules.
	for key, o := range m.objects {
		if key == "v3/store.json" || strings.HasSuffix(key, "/checkpoint.json") {
			var document map[string]any
			if err := json.Unmarshal(o.body, &document); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"id", "token"} {
				if id, ok := document[field].(string); ok {
					pairs = append(pairs, id, "$"+field)
				}
			}
		}
	}
	ids := strings.NewReplacer(pairs...)
	normalize := func(body []byte) string { return ids.Replace(normalise(body, w, dir)) }
	bodies := map[string]string{}
	// Dependencies point from manifests to file/launch descriptors and sets, and from
	// launches to sets. Normalize in that topological order to recompute path-only sizes.
	slices.SortStableFunc(items, func(a, b item) int { return a.rank - b.rank })
	for _, item := range items {
		text := normalize(m.objects[item.key].body)
		var document map[string]any
		if err := json.Unmarshal([]byte(text), &document); err != nil {
			t.Fatal(err)
		}
		dependencies := listManifest(document["dependencies"])
		if document["kind"] != "file" {
			// The real record's set ordering was asserted by DecodeRecord and Audit.
			// Remapping path-derived object hashes may change lexical order, so sort
			// only the normalized set view; never reorder content pieces.
			slices.SortFunc(dependencies, func(a, b any) int {
				return strings.Compare(a.(map[string]any)["object"].(string), b.(map[string]any)["object"].(string))
			})
		}
		for _, v := range dependencies {
			dep := v.(map[string]any)
			if body, ok := bodies[dep["object"].(string)]; ok {
				dep["size"] = len(body)
			}
		}
		body, err := jsonx.Compact(document)
		if err != nil {
			t.Fatal(err)
		}
		bodies[normalize([]byte(item.key))] = string(body)
		if strings.Contains(item.key, "/commits/") {
			bodies[normalize([]byte(strings.Replace(item.key, "/commits/", "/plans/", 1)))] = string(body)
		}
	}
	for key, o := range m.objects {
		if key == "v3/store.json" || strings.HasSuffix(key, "/checkpoint.json") {
			bodies[normalize([]byte(key))] = normalize(o.body)
		}
	}
	return ids, bodies
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
func bucketDigest(t *testing.T, m *memStore, bodies map[string]string) []string {
	t.Helper()
	var out []string
	for k, o := range m.objects {
		if strings.Contains(k, "/records/") || strings.HasPrefix(k, "v3/snapshots/sets/") || k == "v3/store.json" || strings.HasSuffix(k, "/checkpoint.json") {
			// render normalizes the key; lookup must use the same normalized key.
			// Bodies are supplied below as separate entries so no real metadata bytes vanish.
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
	for key, body := range bodies {
		if strings.Contains(key, "/plans/") {
			out = append(out, fmt.Sprintf("%s size=%d same-descriptor=%s", key, len(body), strings.Replace(key, "/plans/", "/commits/", 1)))
		} else {
			out = append(out, key+"\n"+body)
		}
	}
	return out
}

// Golden may reorder a normalized dependency set only after the actual wire bytes
// prove sorted uniqueness. These records must fail before normalization can hide them.
func TestGoldenRejectsUnsortedAndDuplicateWireDependencies(t *testing.T) {
	a := testenv.Dependency{Object: "v3/blobs/plain/a", SHA256: raw.SHA256([]byte("a")), Size: 1, Encoding: "plain"}
	b := testenv.Dependency{Object: "v3/blobs/plain/b", SHA256: raw.SHA256([]byte("b")), Size: 1, Encoding: "plain"}
	for _, c := range []struct {
		name         string
		dependencies []testenv.Dependency
	}{
		{"unsorted", []testenv.Dependency{b, a}},
		{"duplicate", []testenv.Dependency{a, a}},
	} {
		t.Run(c.name, func(t *testing.T) {
			record := testenv.Record{Schema: 3, MachineID: "bench", Kind: "root", Agent: "claude", Key: "root", Sequence: 1, Root: map[string]any{"path": "/root", "agent": "claude"}, Dependencies: c.dependencies}
			body, err := jsonx.SortedCompact(record)
			if err != nil {
				t.Fatal(err)
			}
			key := testenv.Stream("bench", "root", "claude", "root") + fmt.Sprintf("commits/%020d-%s.json", 1, raw.SHA256(body))
			if _, err := testenv.DecodeRecord(key, body); err == nil {
				t.Fatal("wire dependency disorder accepted before normalization")
			}
		})
	}
}
