package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/testenv"
)

var simulations = flag.Int("simulations", 8, "seeded session histories TestSimulatedSessions plays")

const simSteps = 40

// TestSimulatedSessions plays seeded histories of what agents and machines do to session files
// (turns, half-written lines, rewrites, truncation, deletion, recompression, new sessions, odd
// bytes, secrets in config) between sweeps and hook syncs, some of which fail or die part way.
// After every sync that completes, everything on disk must be readable back from the bucket as
// docs/storage-layout.md describes, and every earlier upload a change replaced must be kept as a
// version.
func TestSimulatedSessions(t *testing.T) {
	for seed := range *simulations {
		t.Run(strconv.Itoa(seed), func(t *testing.T) { newSim(t, uint64(seed)).play() })
	}
}

type sim struct {
	t      *testing.T
	rng    *rand.Rand
	w      *testenv.Workload
	r      *rig
	faults *faultyStore
	// tracked are the files whose bytes the bucket must hold; deleted those it must tombstone.
	tracked map[string]bool
	deleted map[string]bool
	// uploaded is what the bucket held for a key after the last complete sync.
	uploaded map[string][]byte
	secrets  []string
	broken   string
	n        int
}

func newSim(t *testing.T, seed uint64) *sim {
	if seed%2 == 1 {
		SetLimits(t, 32<<10, 4<<10, snapshotRecheck) // odd seeds stream every larger transcript
	}
	dir := resolvedTempDir(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(seed) * time.Hour)
	w := testenv.NewWorkload(t, dir, testenv.SmallProfile, start.Add(-2*time.Hour))
	r := newRig(t, w, filepath.Join(dir, "state"))
	r.now = start
	s := &sim{
		t: t, rng: rand.New(rand.NewPCG(seed, 99)), w: w, r: r,
		tracked: map[string]bool{}, deleted: map[string]bool{}, uploaded: map[string][]byte{},
		secrets: []string{"sk-ant-not-a-real-key", "ghp_not_a_real_token"},
	}
	s.faults = &faultyStore{memStore: r.bucket}
	r.store = s.faults
	for _, p := range w.Mains {
		s.tracked[p] = true
	}
	for _, rel := range []string{".claude/history.jsonl", ".claude/plans/quiet-plan.md", ".codex/history.jsonl"} {
		s.tracked[filepath.Join(w.Home, rel)] = true
	}
	return s
}

func (s *sim) play() {
	s.sync("backfill", nil)
	for range simSteps {
		for range 1 + s.rng.IntN(3) {
			s.change()
		}
		s.r.now = s.r.now.Add(s.pause())
		s.syncSomehow()
	}
	s.r.now = s.r.now.Add(2 * time.Hour)
	s.sync("final sweep", nil)
}

func (s *sim) pause() time.Duration {
	switch s.rng.IntN(6) {
	case 0:
		return time.Duration(1+s.rng.IntN(50)) * time.Second // inside the partial-line window
	case 1:
		return foldAfter + time.Duration(s.rng.IntN(3600))*time.Second
	default:
		return time.Duration(1+s.rng.IntN(600)) * time.Second
	}
}

// pick returns a tracked file an agent still writes to, which Codex's compressed rollouts aren't.
func (s *sim) pick() string {
	var live []string
	for p := range s.tracked {
		if !strings.HasSuffix(p, ".zst") {
			live = append(live, p)
		}
	}
	slices.Sort(live)
	return live[s.rng.IntN(len(live))]
}

func (s *sim) mains() []string {
	var out []string
	for p := range s.tracked {
		if isTranscript(p) && !strings.HasSuffix(p, ".zst") {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func (s *sim) write(path string, data []byte) {
	s.t.Helper()
	testenv.Write(s.t, path, string(data))
	s.touch(path)
}

func (s *sim) touch(path string) {
	s.t.Helper()
	if err := os.Chtimes(path, s.r.now, s.r.now); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sim) read(path string) []byte {
	s.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		s.t.Fatal(err)
	}
	return data
}

func (s *sim) appendText(path, text string) {
	s.t.Helper()
	testenv.Append(s.t, path, text)
	s.touch(path)
}

// change does one thing an agent, its user or the machine does to the files.
func (s *sim) change() {
	s.n++
	mains := s.mains()
	main := mains[s.rng.IntN(len(mains))]
	op := s.rng.IntN(16)
	switch op {
	case 0, 1, 2, 3:
		s.w.AppendTurn(s.t, main, 512+s.rng.IntN(16<<10), s.r.now)
	case 4:
		s.appendText(main, `{"type":"assistant","message":{"content":"half wr`)
	case 5:
		if data := s.read(main); len(data) > 0 && data[len(data)-1] != '\n' {
			s.appendText(main, "itten\"}}\n")
		}
	case 6:
		s.appendText(main, oddLine(s.rng))
	case 7:
		s.rewrite(s.pick())
	case 8:
		s.truncate(s.pick())
	case 9:
		s.remove(s.pick())
	case 10:
		s.newSession()
	case 11:
		s.recompress()
	case 12:
		s.touch(s.pick())
	case 13:
		s.secret()
	case 14:
		s.brokenConfig()
	default:
		s.appendText(main, `{"type":"user","message":{"content":"`+strings.Repeat("x", 9<<10+s.rng.IntN(64<<10))+`"}}`+"\n")
	}
}

func oddLine(rng *rand.Rand) string {
	lines := []string{
		`{"type":"user","message":{"content":"naïve café 日本語 🚀"}}` + "\n",
		`{"type":"user","message":{"content":"windows"}}` + "\r\n",
		"{\"type\":\"user\",\"bytes\":\"\x00\xff\xfe\"}\n",
		"\n",
		`{"type":"user","message":{"content":"` + strings.Repeat("\\n", 500) + `"}}` + "\n",
	}
	return lines[rng.IntN(len(lines))]
}

func (s *sim) rewrite(path string) {
	data := s.read(path)
	if len(data) == 0 {
		s.write(path, []byte("{}\n"))
		return
	}
	i := s.rng.IntN(len(data))
	data = slices.Concat(data[:i], []byte(`{"rewritten":true}`), data[i:])
	s.write(path, data)
}

func (s *sim) truncate(path string) {
	data := s.read(path)
	cut := bytes.LastIndexByte(data[:len(data)/2], '\n') + 1
	s.write(path, data[:cut])
}

func (s *sim) remove(path string) {
	if len(s.mains()) < 2 && isTranscript(path) {
		return
	}
	if err := os.Remove(path); err != nil {
		s.t.Fatal(err)
	}
	delete(s.tracked, path)
	s.deleted[path] = true
}

func (s *sim) newSession() {
	id := fmt.Sprintf("%08x-0000-4000-8000-%012x", s.n, s.rng.Uint64()&0xffffffffffff)
	var path, head string
	switch s.rng.IntN(3) {
	case 0:
		path = filepath.Join(s.w.Home, ".claude", "projects", "-home-dev-code-proj-9", id+".jsonl")
		head = `{"type":"user","sessionId":"` + id + `","cwd":"/home/dev/code/proj-9","message":{"content":"new"}}`
	case 1:
		path = filepath.Join(s.w.Home, ".codex", "sessions", "2026", "10", "02", "rollout-2026-10-02T10-00-00-"+id+".jsonl")
		head = `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/home/dev/code/proj-9"}}`
	default:
		path = filepath.Join(s.w.Home, ".pi", "agent", "sessions", "--home-dev-code-proj-9--", "2026-10-02T10-00-00-000Z_"+id+".jsonl")
		head = `{"type":"session","version":3,"id":"` + id + `","cwd":"/home/dev/code/proj-9"}`
	}
	s.write(path, []byte(head+"\n"))
	s.tracked[path] = true
}

// recompress does what Codex does to an old rollout: replaces it by a .zst of the same bytes.
func (s *sim) recompress() {
	for _, p := range s.mains() {
		if strings.Contains(p, "/.codex/") && strings.HasSuffix(p, ".jsonl") {
			s.write(p+".zst", raw.Compress(s.read(p)))
			if err := os.Remove(p); err != nil {
				s.t.Fatal(err)
			}
			delete(s.tracked, p)
			s.tracked[p+".zst"] = true
			return
		}
	}
}

func (s *sim) secret() {
	value := fmt.Sprintf("sk-ant-sim-%d-%x", s.n, s.rng.Uint64())
	s.secrets = append(s.secrets, value)
	s.write(filepath.Join(s.w.Home, ".claude", "settings.json"),
		[]byte(`{"model": "opus", "env": {"ANTHROPIC_API_KEY": "`+value+`", "LOG": "1"}}`))
}

// brokenConfig leaves a config file half written, as an editor that crashed would; the next
// change fixes it.
func (s *sim) brokenConfig() {
	path := filepath.Join(s.w.Home, ".claude", "settings.local.json")
	if s.broken == "" {
		value := fmt.Sprintf("sk-ant-half-%d", s.n)
		s.secrets = append(s.secrets, value)
		s.write(path, []byte(`{"env": {"KEY": "`+value))
		s.broken = path
		return
	}
	s.write(path, []byte(`{"env": {"KEY": "${KEY}"}}`))
	s.broken = ""
}

// syncSomehow runs a sweep or a hook's sync, and sometimes makes the bucket fail or the
// process die part way, after which the next complete sync must have caught up.
func (s *sim) syncSomehow() {
	mains := s.mains()
	main := mains[s.rng.IntN(len(mains))]
	var paths []string
	if s.rng.IntN(2) == 0 {
		paths = []string{main}
	}
	switch s.rng.IntN(9) {
	case 0:
		s.faults.fail = 10 + s.rng.IntN(40)
		s.syncUnchecked(paths)
		s.sync("after failures", nil)
	case 1:
		s.faults.fail, s.faults.lose = 20+s.rng.IntN(60), 5+s.rng.IntN(20)
		s.syncUnchecked(paths)
		if s.rng.IntN(2) == 0 {
			s.change() // the session carried on before the next sync
		}
		s.sync("after lost answers", nil)
	case 2:
		s.kill(paths)
		s.sync("after a kill", nil)
	default:
		s.sync("step", paths)
	}
}

// kill runs a sync that dies part way, as a hook's does when the agent stops waiting for it:
// the requests sent by then reach the bucket and the state on disk is what had been written.
func (s *sim) kill(paths []string) {
	s.t.Helper()
	db := filepath.Join(s.r.cfg.StateDir, "state.sqlite3")
	saved := filepath.Join(s.t.TempDir(), "state.sqlite3")
	files := []string{"", "-wal"}
	s.faults.killAt = 1 + s.rng.IntN(4)
	s.faults.onKill = func() {
		for _, f := range files {
			if data, err := os.ReadFile(db + f); err == nil {
				testenv.Write(s.t, saved+f, string(data))
			}
		}
	}
	s.syncUnchecked(paths)
	if !fsutil.IsFile(saved) {
		return // it finished before the kill
	}
	for _, f := range append(files, "-shm") {
		if err := os.Remove(db + f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.t.Fatal(err)
		}
		if data, err := os.ReadFile(saved + f); err == nil {
			testenv.Write(s.t, db+f, string(data))
		}
	}
}

func (s *sim) syncUnchecked(paths []string) {
	defer s.faults.reset()
	s.run(paths)
}

func (s *sim) run(paths []string) *Summary {
	s.t.Helper()
	s.faults.round++
	if len(paths) == 0 {
		return s.r.sync("")
	}
	return s.r.sync(s.agentOf(paths[0]), paths...)
}

func (s *sim) agentOf(path string) string {
	switch {
	case strings.Contains(path, "/.claude/"):
		return "claude"
	case strings.Contains(path, "/.codex/"):
		return "codex"
	default:
		return "pi"
	}
}

// sync runs a sync that must complete, then checks the bucket.
func (s *sim) sync(name string, paths []string) {
	s.t.Helper()
	sum := s.run(paths)
	want := 0
	if s.broken != "" && len(paths) == 0 {
		want = 1
	}
	if sum.Errors > want {
		s.t.Fatalf("step %d %s: %d errors: %v", s.n, name, sum.Errors, sum.ErrorMessages)
	}
	if len(paths) > 0 {
		return // a hook's sync only covers its own session
	}
	s.check(name)
	if again := s.run(nil); again.Uploaded != 0 {
		s.t.Errorf("step %d %s: a sync straight after uploaded %d files again", s.n, name, again.Uploaded)
	}
}

// expected is what the bucket must hold for path: the file's bytes, decompressed if Codex
// compressed it, without a last line still being written.
func (s *sim) expected(path string) []byte {
	data := s.read(path)
	if strings.HasSuffix(path, ".zst") {
		var err error
		if data, err = raw.Decompress(data); err != nil {
			s.t.Fatal(err)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		s.t.Fatal(err)
	}
	recent := s.r.now.Sub(st.ModTime()) < partialLineWindow
	if filepath.Ext(path) == ".jsonl" && recent && !bytes.HasSuffix(data, []byte("\n")) {
		data = data[:bytes.LastIndexByte(data, '\n')+1]
	}
	return data
}

// keyOf is the documented key (docs/storage-layout.md) for a file under an agent's directory.
func (s *sim) keyOf(path string) string {
	for agent, dir := range map[string]string{"claude": ".claude", "codex": ".codex", "pi": ".pi/agent"} {
		rel, err := filepath.Rel(filepath.Join(s.w.Home, dir), path)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		rel = filepath.ToSlash(rel)
		if agent == "codex" && strings.HasPrefix(rel, "sessions/") {
			rel = "rollouts/" + strings.TrimSuffix(filepath.Base(rel), ".zst")
		}
		return "raw/agent=" + agent + "/machine=bench/" + rel
	}
	s.t.Fatalf("%s is under no agent directory", path)
	return ""
}

func (s *sim) check(name string) {
	s.t.Helper()
	b := s.r.bucket
	for _, path := range slices.Sorted(maps.Keys(s.tracked)) {
		want := s.expected(path)
		if len(want) == 0 {
			continue
		}
		key := s.keyOf(path)
		got, err := rebuild(b, key)
		if err != nil || !bytes.Equal(got, want) {
			s.t.Fatalf("step %d %s: %s holds %d bytes (%v), the file %d", s.n, name, key, len(got), err, len(want))
		}
		if old, ok := s.uploaded[key]; ok && !bytes.HasPrefix(want, old) {
			if !hasVersion(b, key, old) {
				s.t.Fatalf("step %d %s: %s was replaced without keeping its %d earlier bytes", s.n, name, key, len(old))
			}
		}
		s.uploaded[key] = want
	}
	s.checkManifests(name)
	for path := range s.deleted {
		if _, err := os.Stat(path); err == nil || s.uploaded[s.keyOf(path)] == nil {
			continue
		}
		s.checkTombstone(name, path)
	}
	for k, o := range b.objects {
		body := o.body
		if strings.HasSuffix(k, ".zst") {
			body, _ = raw.Decompress(body)
		}
		for _, secret := range s.secrets {
			if bytes.Contains(body, []byte(secret)) {
				s.t.Fatalf("step %d %s: %s holds the secret %s", s.n, name, k, secret)
			}
		}
	}
}

// checkManifests rebuilds every transcript a manifest lists from its pieces, each of which must
// hold the bytes its sha256 names, and compares those still on disk with the file.
func (s *sim) checkManifests(name string) {
	s.t.Helper()
	files := map[string]string{}
	for path := range s.tracked {
		files[s.keyOf(path)+".zst"] = path
	}
	for k, o := range s.r.bucket.objects {
		if !strings.HasPrefix(k, "manifests/") {
			continue
		}
		var m struct {
			Transcripts []struct {
				Object string      `json:"object"`
				Pieces []raw.Piece `json:"pieces"`
			} `json:"transcripts"`
		}
		if err := json.Unmarshal(o.body, &m); err != nil {
			s.t.Fatal(err)
		}
		for _, tr := range m.Transcripts {
			got, err := fromPieces(s.r.bucket, tr.Pieces)
			if err != nil {
				s.t.Fatalf("step %d %s: %s in %s: %v", s.n, name, tr.Object, k, err)
			}
			path, ok := files[tr.Object]
			if !ok {
				continue
			}
			if want := s.expected(path); len(want) > 0 && !bytes.Equal(got, want) {
				s.t.Fatalf("step %d %s: %s lists %d bytes for %s, the file %d", s.n, name, k, len(got), tr.Object, len(want))
			}
		}
	}
}

func fromPieces(b *memStore, pieces []raw.Piece) ([]byte, error) {
	var out []byte
	for _, p := range pieces {
		o, ok := b.objects[p.Object]
		if !ok {
			return nil, fmt.Errorf("%s is missing", p.Object)
		}
		data, err := raw.Decompress(o.body)
		if err != nil {
			return nil, err
		}
		if int64(len(out)) != p.Start || raw.SHA256(data) != p.SHA256 {
			return nil, fmt.Errorf("%s doesn't hold bytes %d-%d", p.Object, p.Start, p.End)
		}
		out = append(out, data...)
	}
	return out, nil
}

func (s *sim) checkTombstone(name, path string) {
	if !isTranscript(path) || !strings.HasSuffix(path, ".jsonl") {
		return
	}
	key := strings.TrimPrefix(s.keyOf(path), "raw/agent="+s.agentOf(path)+"/machine=bench/")
	for k, o := range s.r.bucket.objects {
		if !strings.HasPrefix(k, "manifests/agent="+s.agentOf(path)+"/") {
			continue
		}
		var m struct {
			Deleted []struct {
				Key string `json:"key"`
			} `json:"deleted_locally"`
		}
		if err := json.Unmarshal(o.body, &m); err != nil {
			s.t.Fatal(err)
		}
		for _, d := range m.Deleted {
			if d.Key == key {
				return
			}
		}
	}
	s.t.Fatalf("step %d %s: no manifest says %s was deleted", s.n, name, key)
}

func isTranscript(path string) bool {
	return strings.Contains(path, "/projects/") || strings.Contains(path, "/sessions/")
}

// rebuild reads a raw file without its manifest: the base object, then each segment that
// starts where the bytes so far end.
func rebuild(b *memStore, key string) ([]byte, error) {
	base, ok := b.objects[key+".zst"]
	if !ok {
		return nil, errors.New("no base object")
	}
	out, err := raw.Decompress(base.body)
	if err != nil {
		return nil, err
	}
	var segments []string
	for k := range b.objects {
		if strings.HasPrefix(k, key+".zst.d/") {
			segments = append(segments, k)
		}
	}
	slices.Sort(segments)
	for _, k := range segments {
		var start, end int64
		if _, err := fmt.Sscanf(strings.TrimPrefix(k, key+".zst.d/"), "%d-%d.zst", &start, &end); err != nil {
			return nil, err
		}
		if start != int64(len(out)) {
			continue
		}
		data, err := raw.Decompress(b.objects[k].body)
		if err != nil {
			return nil, err
		}
		out = append(out, data...)
	}
	return out, nil
}

// hasVersion says whether one of the key's versions, rebuilt from its blobs, starts with old:
// appends a hook uploaded after old was checked are part of the version that replaced it.
func hasVersion(b *memStore, key string, old []byte) bool {
	index, ok := b.objects["raw-versions/"+strings.TrimPrefix(key, "raw/")+".json"]
	if !ok {
		return false
	}
	var doc struct {
		Versions []struct {
			SHA256 string      `json:"sha256"`
			Pieces []raw.Piece `json:"pieces"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(index.body, &doc); err != nil {
		return false
	}
	for _, v := range doc.Versions {
		var got []byte
		for _, p := range v.Pieces {
			o, ok := b.objects[p.Object]
			if !ok {
				return false
			}
			data, err := raw.Decompress(o.body)
			if err != nil || raw.SHA256(data) != p.SHA256 {
				return false
			}
			got = append(got, data...)
		}
		if raw.SHA256(got) != v.SHA256 {
			return false
		}
		if bytes.HasPrefix(got, old) {
			return true
		}
	}
	return false
}

var errFault = errors.New("injected bucket failure")

// faultyStore makes requests fail, either before they land or, as when the process dies or
// the connection drops mid-answer, after. Which requests fail depends only on the round and
// the request, not on the order concurrent uploads reach it.
type faultyStore struct {
	*memStore
	round      uint64
	fail, lose int // out of 100
	// At request killAt, onKill runs and the process dies: no request from then on is sent.
	mu     sync.Mutex
	killAt int
	sent   int
	onKill func()
}

func (f *faultyStore) reset() {
	f.fail, f.lose, f.killAt, f.sent = 0, 0, 0, 0
}

func (f *faultyStore) dead() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.killAt == 0 {
		return false
	}
	f.sent++
	if f.sent == f.killAt {
		f.onKill()
	}
	return f.sent >= f.killAt
}

func (f *faultyStore) fault(op string, do func() error) error {
	if f.dead() {
		return errFault
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d %s", f.round, op) // hash writes never fail
	switch n := int(h.Sum64() % 100); {
	case n < f.fail:
		return errFault
	case n < f.fail+f.lose:
		if err := do(); err != nil {
			return err
		}
		return errFault
	}
	return do()
}

func (f *faultyStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	return f.fault("put "+key, func() error { return f.memStore.Put(ctx, key, body, meta) })
}

func (f *faultyStore) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	return f.fault("put "+key, func() error { return f.memStore.PutFile(ctx, key, path, meta) })
}

func (f *faultyStore) Copy(ctx context.Context, src, dst, sha string) (bool, error) {
	var ok bool
	err := f.fault("copy "+dst, func() error {
		var err error
		ok, err = f.memStore.Copy(ctx, src, dst, sha)
		return err
	})
	return ok, err
}

func (f *faultyStore) Delete(ctx context.Context, key string) error {
	return f.fault("delete "+key, func() error { return f.memStore.Delete(ctx, key) })
}
