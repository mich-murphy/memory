package syncer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/syncer"
	"github.com/mich-murphy/memory/internal/testenv"
)

var errInjected = errors.New("injected failure")

// flakyStore fails writes to keys containing any of fail, puts to keys containing any of lost
// after they land, as when the answer to a request never arrives, and deletes of keys
// containing any of undeletable.
type flakyStore struct {
	store.Store
	fail, lost []string
	mu         sync.Mutex
	hits       int
	deletes    []string
}

func (f *flakyStore) answer(key string, err error) error {
	if err == nil && containsAny(key, f.lost) {
		f.hit()
		return errInjected
	}
	return err
}

func (f *flakyStore) broken(key string) bool {
	if containsAny(key, f.fail) {
		f.hit()
		return true
	}
	return false
}

func containsAny(key string, parts []string) bool {
	return slices.ContainsFunc(parts, func(s string) bool { return strings.Contains(key, s) })
}

func (f *flakyStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if f.broken(key) {
		return errInjected
	}
	return f.answer(key, f.Store.Put(ctx, key, body, meta))
}

func (f *flakyStore) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	if f.broken(key) {
		return errInjected
	}
	return f.answer(key, f.Store.PutFile(ctx, key, path, meta))
}

func (f *flakyStore) Copy(ctx context.Context, src, dst, sha string) (bool, error) {
	if f.broken(dst) {
		return false, errInjected
	}
	return f.Store.Copy(ctx, src, dst, sha)
}

func (f *flakyStore) hit() { f.mu.Lock(); defer f.mu.Unlock(); f.hits++ }

func (f *flakyStore) assertFired(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits == 0 {
		t.Fatal("injected protocol fault never fired")
	}
}

func (f *flakyStore) Create(ctx context.Context, key string, body []byte) error {
	if f.broken(key) {
		return errInjected
	}
	return f.answer(key, f.Store.Create(ctx, key, body))
}

func (f *flakyStore) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, key)
	return errors.New("v3 archive attempted remote deletion")
}

// sweep runs one sweep through the syncer API with st as the bucket.
func sweep(t *testing.T, st store.Store) *syncer.Summary {
	t.Helper()
	return sweepFound(t.Context(), t, st, func([]adapters.SessionFile) {})
}

// sweepFound runs a sweep under ctx that hands the files it discovers to found before reading
// them, for what happens in between.
func sweepFound(ctx context.Context, t *testing.T, st store.Store, found func([]adapters.SessionFile)) *syncer.Summary {
	t.Helper()
	if faults, ok := st.(*flakyStore); ok {
		defer func() {
			faults.mu.Lock()
			defer faults.mu.Unlock()
			if len(faults.deletes) > 0 {
				t.Errorf("remote deletes attempted: %v", faults.deletes)
			}
		}()
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := state.Open(ctx, cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy := syncer.New(cfg, st, s, adapters.All(cfg.Home), syncer.Options{Trigger: "sweep", Logger: slog.New(slog.DiscardHandler)})
	if err := sy.Recover(ctx); err != nil {
		return &syncer.Summary{Errors: 1, ErrorMessages: []string{err.Error()}}
	}
	files, err := sy.SweepFiles(ctx, adapters.Names)
	if err != nil {
		t.Fatal(err)
	}
	found(files)
	summary, err := sy.Run(ctx, files)
	if err != nil {
		return &syncer.Summary{Errors: 1, ErrorMessages: []string{err.Error()}}
	}
	return summary
}

// TestAFailedUploadIsCountedAndRetriedNextRun fails a file's upload, the Claude manifest's
// snapshot (only its harness holds ~/.claude/CLAUDE.md) and the Codex manifest's upload. The
// manifests sort claude, codex, pi, so the Pi manifest must still be written after both.
func TestAFailedUploadIsCountedAndRetriedNextRun(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	key := fileStream("claude", "plans/brave-plan.md")
	claudeRules := "v3/blobs/plain/" + raw.SHA256(readFile(t, filepath.Join(e.Claude, "CLAUDE.md")))
	manifest := func(agent, session string) string {
		return testenv.Stream(testenv.MachineID, "manifest", agent, session)
	}
	faults := &flakyStore{Store: local, fail: []string{key + "plans/", claudeRules, manifest("codex", codexKey) + "plans/"}}
	first := sweep(t, faults)
	faults.assertFired(t)
	if first.Errors != 3 || len(first.ErrorMessages) != 3 {
		t.Errorf("errors = %d %v", first.Errors, first.ErrorMessages)
	}
	if e.HasStream(key) {
		t.Fatal("failed publication committed anyway")
	}
	if !e.HasStream(fileStream("claude", "history.jsonl")) {
		t.Error("one failure stopped other uploads")
	}
	if first.Manifests != 1 || !e.HasStream(manifest("pi", testenv.PiSessionKey)) {
		t.Errorf("manifests = %d", first.Manifests)
	}
	second := sweep(t, local)
	if second.Errors != 0 || !e.HasStream(key) || !e.HasStream(manifest("claude", claudeKey)) || !e.HasStream(manifest("codex", codexKey)) {
		t.Errorf("retry = %+v", second)
	}
}

func TestAnAppendWhoseAnswerWasLostIsReconciledExactly(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	sweep(t, local)
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := fileStream("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	faults := &flakyStore{Store: local, lost: []string{"v3/blobs/zstd/", key + "commits/"}}
	for _, st := range []store.Store{faults, local} {
		testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": "more"}))
		testenv.Backdate(t, path, 2*time.Minute)
		sweep(t, st)
	}
	faults.assertFired(t)
	if got, want := readObject(t, e, key), readFile(t, path); !bytes.Equal(got, want) {
		t.Errorf("the bucket rebuilds %d bytes of the file's %d", len(got), len(want))
	}
}

// Lost acknowledgements never require deleting immutable content. Both session streams
// must remain readable through retries, and all earlier objects survive every round.
func TestLostAppendAnswersNeverRequireRemoteDeletion(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	sweep(t, local)
	session := "projects/" + slug + "/" + claudeSID
	files := map[string]string{
		filepath.Join(e.ClaudeSlug, claudeSID+".jsonl"):                        fileStream("claude", session+".jsonl"),
		filepath.Join(e.ClaudeSlug, claudeSID, "subagents", "agent-abc.jsonl"): fileStream("claude", session+"/subagents/agent-abc.jsonl"),
	}
	for round := range 5 {
		before := mtimes(t, e)
		faults := &flakyStore{Store: local}
		if round%2 == 0 {
			faults.lost = []string{"v3/blobs/zstd/", "/commits/"}
		}
		for path := range files {
			testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": round}))
			testenv.Backdate(t, path, 2*time.Minute)
		}
		summary := sweep(t, faults)
		if round%2 == 0 {
			faults.assertFired(t)
		}
		if summary.Errors != 0 {
			t.Errorf("round %d: %v", round, summary.ErrorMessages)
		}
		for path, key := range files {
			if got, want := readObject(t, e, key), readFile(t, path); !bytes.Equal(got, want) {
				t.Errorf("round %d: rebuilt content differs for %s", round, path)
			}
		}
		for key, mtime := range before {
			if mtimes(t, e)[key] != mtime {
				t.Errorf("round %d: removed or rewrote %s", round, key)
			}
		}
	}
}

func TestAFoldWhoseAnswerWasLostRemainsReadableAfterAnotherAppend(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	sweep(t, local)
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := fileStream("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": "more"}))
	testenv.Backdate(t, path, 2*time.Minute)
	sweep(t, local)
	appendArchivePieces(t, path, 62)
	faults := &flakyStore{Store: local, lost: []string{key + "commits/", "v3/blobs/zstd/"}}
	sweep(t, faults)
	faults.assertFired(t)
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": "again"}))
	testenv.Backdate(t, path, 2*time.Minute)
	sweep(t, local)
	main := mainTranscript(t, e.Manifest("claude", claudeKey))
	if !bytes.Equal(readPieces(t, e, main["pieces"]), readFile(t, path)) {
		t.Error("the manifest's pieces don't rebuild the file")
	}
}

func TestARewriteWhoseAnswerWasLostStillKeepsTheEarlierBytes(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	sweep(t, local)
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	old := readFile(t, path)
	testenv.Write(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": "rewritten"}))
	testenv.Backdate(t, path, 2*time.Minute)
	faults := &flakyStore{Store: local, lost: []string{fileStream("claude", "projects/"+slug+"/"+claudeSID+".jsonl") + "commits/"}}
	sweep(t, faults)
	faults.assertFired(t)
	sweep(t, local)
	history := e.History("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	if len(history) != 2 || !bytes.Equal(e.GenerationContent(history[0].File), old) {
		t.Error("the blob doesn't hold the earlier bytes")
	}
}

// A whole upload whose answer was lost leaves the base object holding bytes the state doesn't
// know of. A rewrite after it must keep the earlier bytes only from objects that hold them: a
// blob named for bytes it doesn't hold would corrupt every version that shares it.
func TestAVersionAfterALostWholeUploadKeepsOnlyTheRightBytes(t *testing.T) {
	cases := []struct {
		name string
		// segment uploads the append as a segment first, so the whole upload is a fold.
		segment bool
		lost    bool
		kept    string
	}{
		{"foldLanded", true, true, "a\nb\n"},
		{"foldFailed", true, false, "a\nb\n"},
		{"appendLanded", false, true, "a\nb\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := testenv.New(t)
			local := store.NewLocal(e.Bucket)
			path := filepath.Join(e.Claude, "plans", "p.md")
			testenv.Write(t, path, "a\n")
			testenv.Backdate(t, path, time.Hour)
			sweep(t, local)
			testenv.Append(t, path, "b\n")
			if c.segment {
				sweep(t, local)
				appendArchivePieces(t, path, 62)
			}
			keptContent := string(readFile(t, path))
			testenv.Backdate(t, path, time.Hour)
			prefix := fileStream("claude", "plans/p.md")
			flaky := &flakyStore{Store: local, fail: []string{prefix + "commits/"}}
			if c.lost {
				flaky = &flakyStore{Store: local, lost: []string{prefix + "commits/", "v3/blobs/zstd/"}}
			}
			sweep(t, flaky)
			flaky.assertFired(t)
			testenv.Write(t, path, "X\n")
			testenv.Backdate(t, path, time.Hour)
			s := sweep(t, local)

			history := e.Records(prefix)
			if s.Errors != 0 || len(history) < 3 {
				t.Fatalf("retry = %+v, history count %d", s, len(history))
			}
			kept := false
			for _, r := range history[:len(history)-1] {
				if string(e.GenerationContent(r.File)) == keptContent {
					kept = true
				}
			}
			if !kept {
				t.Errorf("history lost acknowledged %q", keptContent)
			}
			if string(e.GenerationContent(history[len(history)-1].File)) != "X\n" {
				t.Error("rewrite not current")
			}
		})
	}
}

func TestRewriteWhoseOldBlobIsGoneFailsClosed(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	key := fileStream("claude", "plans/brave-plan.md")
	history := e.Records(key)
	blob := history[0].File.Pieces[0].Object
	if err := os.Remove(filepath.Join(e.Bucket, blob)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.Claude, "plans", "brave-plan.md")
	testenv.Write(t, path, "# new plan\n")
	testenv.Backdate(t, path, 2*time.Minute)
	if code, _ := run(t, "sync", "--quiet"); code == 0 {
		t.Error("missing archived bytes went unreported")
	}
	// No successor may silently bless a chain with missing historical content.
	if got := e.Records(key); len(got) != 1 || got[0].ID != history[0].ID {
		t.Errorf("corrupt history advanced: %v", got)
	}
}

func TestLargeFileBeingWrittenHoldsBackItsLastLine(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64, 16, 10*time.Minute)
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	complete := readFile(t, path)
	testenv.Append(t, path, `{"type":"message","id":"half`)
	mustRun(t, "sync", "--quiet")
	key := fileStream("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
	if !bytes.Equal(readObject(t, e, key), complete) {
		t.Error("half a line was uploaded")
	}
	testenv.Append(t, path, "\"}\n")
	mustRun(t, "sync", "--quiet")
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("completed line missing")
	}
}

func TestAFileStillWritingItsFirstLineIsSkipped(t *testing.T) {
	for name, largeFile := range map[string]int64{"small": 64 << 20, "large": 8} {
		t.Run(name, func(t *testing.T) {
			e := testenv.New(t)
			syncer.SetLimits(t, largeFile, 16, 10*time.Minute)
			path := filepath.Join(e.PiDir, "2026-10-10T13-00-00-000Z_0199a1b2-1111-7bbb-8ccc-dddddddddddd.jsonl")
			testenv.Write(t, path, `{"type":"session","id":"0199a1b2-1111`)
			if s := sweep(t, store.NewLocal(e.Bucket)); s.Skipped != 1 || s.Errors != 0 {
				t.Errorf("summary = %+v", s)
			}
			key := fileStream("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
			if e.HasKey(key) {
				t.Error("a partial first line was uploaded")
			}
		})
	}
}

// TestSnapshotsAreRecheckedAndOnlyAddedWhenSomethingChanged takes a turn after each change and
// checks what the new snapshot, if any, says changed. A change made before a recheck is due
// waits for it.
func TestSnapshotsAreRecheckedAndOnlyAddedWhenSomethingChanged(t *testing.T) {
	e := testenv.New(t)
	session := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	turn := func() []any {
		t.Helper()
		testenv.Append(t, session, testenv.JSONL(t, map[string]any{"type": "user"}))
		mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", session)
		return list(e.Manifest("claude", claudeKey)["snapshots"])
	}
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", session)
	testenv.Write(t, filepath.Join(e.Claude, "CLAUDE.md"), "user rules, edited\n")
	if n := len(turn()); n != 1 {
		t.Fatalf("%d snapshots before a recheck was due", n)
	}
	syncer.SetLimits(t, 64<<20, 1<<20, 0)
	for _, step := range []struct {
		name   string
		change func()
		want   string // what the new snapshot changed, or "nothing added"
	}{
		{"harness edited", func() {}, "id"},
		{"unchanged", func() {}, "nothing added"},
		{"commit", func() {
			testenv.Write(t, filepath.Join(e.Project, "NEW.md"), "x\n")
			testenv.Git(t, e.Project, "add", "-A")
			testenv.Git(t, e.Project, "commit", "-q", "-m", "second")
		}, "head"},
		{"memory cleared", func() {
			if err := os.RemoveAll(filepath.Join(e.ClaudeSlug, "memory")); err != nil {
				t.Fatal(err)
			}
		}, "memory_id"},
	} {
		before := len(list(e.Manifest("claude", claudeKey)["snapshots"]))
		step.change()
		snaps := turn()
		got := "nothing added"
		if len(snaps) > before {
			var changed []string
			a, b := snaps[before-1].(map[string]any), snaps[len(snaps)-1].(map[string]any)
			for _, field := range []string{"id", "memory_id"} {
				if a[field] != b[field] {
					changed = append(changed, field)
				}
			}
			if a["git"].(map[string]any)["head"] != b["git"].(map[string]any)["head"] {
				changed = append(changed, "head")
			}
			got = strings.Join(changed, ",")
		}
		if len(snaps) > before+1 || got != step.want {
			t.Errorf("%s: %d snapshots after %d, the new one changed %q, want %q", step.name, len(snaps), before, got, step.want)
		}
	}
}

func TestARewriteIsHeldBackUntilItsOldBytesAreKept(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	mustRun(t, "sync", "--quiet")
	key := fileStream("claude", "plans/brave-plan.md")
	old := readObject(t, e, key)
	path := filepath.Join(e.Claude, "plans", "brave-plan.md")
	testenv.Write(t, path, "# new plan\n")
	testenv.Backdate(t, path, 2*time.Minute)
	faults := &flakyStore{Store: local, fail: []string{"v3/blobs/zstd/"}}
	if s := sweep(t, faults); s.Errors != 1 {
		t.Errorf("errors = %v", s.ErrorMessages)
	}
	faults.assertFired(t)
	if !bytes.Equal(readObject(t, e, key), old) {
		t.Fatal("rewrite uploaded before the bytes it replaces were kept")
	}
	if s := sweep(t, local); s.Errors != 0 || s.Versions != 1 {
		t.Errorf("retry: %+v", s)
	}
	if string(readObject(t, e, key)) != "# new plan\n" || !hasPrefix(e.BucketKeys(), "v3/blobs/zstd/") {
		t.Errorf("after retry: %v", e.BucketKeys())
	}
}

func TestAnUnreachableBucketChangesNothingThenCatchesUp(t *testing.T) {
	e := testenv.New(t)
	down := sweep(t, &flakyStore{Store: store.NewLocal(e.Bucket), fail: []string{""}})
	if down.Errors == 0 || down.Uploaded != 0 || down.Manifests != 0 {
		t.Errorf("while down: %+v", down)
	}
	if keys := e.BucketKeys(); len(keys) != 0 {
		t.Fatalf("bucket has %v", keys)
	}
	up := sweep(t, store.NewLocal(e.Bucket))
	if up.Errors != 0 || up.Uploaded == 0 || up.Manifests != 3 {
		t.Errorf("back up: %+v", up)
	}
	if again := sweep(t, store.NewLocal(e.Bucket)); again.Uploaded != 0 || again.Manifests != 0 || again.Errors != 0 {
		t.Errorf("third sweep: %+v", again)
	}
}

// TestACanceledSyncUploadsNothing cancels a sweep once it has found its files, as a hook's
// time limit does.
func TestACanceledSyncUploadsNothing(t *testing.T) {
	e := testenv.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	var before []string
	s := sweepFound(ctx, t, store.NewLocal(e.Bucket), func([]adapters.SessionFile) { before = e.BucketKeys(); cancel() })
	if keys := e.BucketKeys(); s.Uploaded != 0 || !slices.Equal(keys, before) {
		t.Errorf("uploaded %d files: %v", s.Uploaded, keys)
	}
}

func TestALeftoverStagedCopyIsRemoved(t *testing.T) {
	e := testenv.New(t)
	left := filepath.Join(e.State, "tmp", "123.zst")
	testenv.Write(t, left, "a transcript a killed run was staging")
	sweep(t, store.NewLocal(e.Bucket))
	if _, err := os.Stat(left); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("staged copy still there: %v", err)
	}
}

func TestALargeFileThatCannotBeStagedIsRetried(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64, 16, 10*time.Minute)
	tmp := filepath.Join(e.State, "tmp")
	testenv.Write(t, tmp, "not a directory")
	if s := sweep(t, store.NewLocal(e.Bucket)); s.Errors == 0 {
		t.Error("staging into a file succeeded")
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	if s := sweep(t, store.NewLocal(e.Bucket)); s.Errors != 0 {
		t.Errorf("retry: %v", s.ErrorMessages)
	}
	if left, err := os.ReadDir(tmp); err != nil || len(left) != 0 {
		t.Errorf("staged files left behind: %v %v", left, err)
	}
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	key := fileStream("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("large file not uploaded on retry")
	}
}

func TestAConfigFileThatCannotBeCheckedForSecretsIsNotUploaded(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Claude, "settings.local.json"), `{"env": {"API_KEY": "sk-half-written`)
	run(t, "sync", "--quiet")
	summary := lastRunSummary(t, e)
	if summary["errors"] != 1.0 || !strings.Contains(fmt.Sprint(summary["error_messages"]), "settings.local.json") {
		t.Errorf("errors = %v %v", summary["errors"], summary["error_messages"])
	}
	if e.HasStream(fileStream("claude", "settings.local.json")) {
		t.Error("uploaded a config file it couldn't check")
	}
	if !e.HasStream(fileStream("claude", "settings.json")) {
		t.Error("one unparseable file stopped the others")
	}
}

// appendArchivePieces reaches the real 64-piece boundary using ordinary publications.
func appendArchivePieces(t *testing.T, path string, count int) {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	ads := adapters.All(cfg.Home)
	sf, ok := ads["claude"].Classify(path, ads["claude"].CanonicalRoot())
	if !ok {
		t.Fatal("cannot classify append fixture")
	}
	for i := range count {
		testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": fmt.Sprintf("piece %d", i)}))
		testenv.Backdate(t, path, 2*time.Minute)
		sy := syncer.New(cfg, store.NewLocal(cfg.LocalStore), db, ads, syncer.Options{Logger: slog.New(slog.DiscardHandler)})
		sum, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
		if err != nil || sum.Errors != 0 || sum.Segments != 1 {
			t.Fatalf("append fixture = %+v, %v", sum, err)
		}
	}
}
