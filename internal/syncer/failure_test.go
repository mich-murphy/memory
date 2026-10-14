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
	fail, lost, undeletable []string
}

func (f *flakyStore) answer(key string, err error) error {
	if err == nil && containsAny(key, f.lost) {
		return errInjected
	}
	return err
}

func (f *flakyStore) broken(key string) bool {
	return containsAny(key, f.fail)
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

func (f *flakyStore) Delete(ctx context.Context, key string) error {
	if containsAny(key, f.undeletable) {
		return errInjected
	}
	return f.Store.Delete(ctx, key)
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
	files, err := sy.SweepFiles(ctx, adapters.Names)
	if err != nil {
		t.Fatal(err)
	}
	found(files)
	summary, err := sy.Run(ctx, files)
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

// TestAFailedUploadIsCountedAndRetriedNextRun fails a file's upload, the Claude manifest's
// snapshot (only its harness holds ~/.claude/CLAUDE.md) and the Codex manifest's upload. The
// manifests sort claude, codex, pi, so the Pi manifest must still be written after both.
func TestAFailedUploadIsCountedAndRetriedNextRun(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	key := rawKey("claude", "plans/brave-plan.md")
	claudeRules := "snapshots/blobs/" + raw.SHA256(readFile(t, filepath.Join(e.Claude, "CLAUDE.md")))
	manifest := func(agent, session string) string {
		return "manifests/agent=" + agent + "/machine=testbox/" + session + ".json"
	}
	first := sweep(t, &flakyStore{Store: local, fail: []string{"plans/brave-plan.md", claudeRules, "manifests/agent=codex"}})
	if first.Errors != 3 || len(first.ErrorMessages) != 3 {
		t.Errorf("errors = %d %v", first.Errors, first.ErrorMessages)
	}
	if e.HasKey(key) {
		t.Fatal("failed upload landed anyway")
	}
	if !e.HasKey(rawKey("claude", "history.jsonl")) {
		t.Error("one failure stopped the other uploads")
	}
	if first.Manifests != 1 || !e.HasKey(manifest("pi", piKey)) {
		t.Errorf("manifests written = %d, keys %v", first.Manifests, e.BucketKeys())
	}
	second := sweep(t, local)
	if second.Errors != 0 || second.Manifests != 2 || !e.HasKey(key) || !e.HasKey(manifest("claude", claudeKey)) ||
		!e.HasKey(manifest("codex", codexKey)) {
		t.Errorf("retry: %d manifests, errors %v, keys %v", second.Manifests, second.ErrorMessages, e.BucketKeys())
	}
}

func TestASegmentWhoseAnswerWasLostIsNotReadAsPartOfTheFile(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	sweep(t, local)
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := rawKey("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	for _, st := range []store.Store{&flakyStore{Store: local, lost: []string{key + ".d/"}}, local} {
		testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": "more"}))
		testenv.Backdate(t, path, 2*time.Minute)
		sweep(t, st)
	}
	if got, want := readObject(t, e, key), readFile(t, path); !bytes.Equal(got, want) {
		t.Errorf("the bucket rebuilds %d bytes of the file's %d", len(got), len(want))
	}
}

// TestASegmentThatCannotBeDeletedMakesItsFileUploadWhole loses the answers to two files'
// segment uploads, then can't delete the segments they left, which start where each file's
// next segment would: each file must be uploaded whole instead. The main transcript's key
// sorts before the subagent's, so the last round has a stray that can't be deleted after one
// that was.
func TestASegmentThatCannotBeDeletedMakesItsFileUploadWhole(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	sweep(t, local)
	session := "projects/" + slug + "/" + claudeSID
	subagent := rawKey("claude", session+"/subagents/agent-abc.jsonl")
	files := map[string]string{
		filepath.Join(e.ClaudeSlug, claudeSID+".jsonl"):                        rawKey("claude", session+".jsonl"),
		filepath.Join(e.ClaudeSlug, claudeSID, "subagents", "agent-abc.jsonl"): subagent,
	}
	lost := &flakyStore{Store: local, lost: []string{".zst.d/"}}
	for _, round := range []struct {
		name string
		st   store.Store
	}{
		{"lost", lost},
		{"undeletable", &flakyStore{Store: local, undeletable: []string{".zst.d/"}}},
		{"clean", local},
		{"lost again", lost},
		{"subagent undeletable", &flakyStore{Store: local, undeletable: []string{subagent + ".d/"}}},
	} {
		for path := range files {
			testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": round.name}))
			testenv.Backdate(t, path, 2*time.Minute)
		}
		sweep(t, round.st)
		for path, key := range files {
			if got, want := readObject(t, e, key), readFile(t, path); !bytes.Equal(got, want) {
				t.Errorf("%s: the bucket rebuilds %d bytes of %s's %d", round.name, len(got), filepath.Base(path), len(want))
			}
		}
	}
}

func TestAFoldWhoseAnswerWasLostIsUploadedWholeAgain(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	sweep(t, local)
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := rawKey("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": "more"}))
	testenv.Backdate(t, path, 2*time.Minute)
	sweep(t, local)
	testenv.Backdate(t, path, syncer.FoldAfter+time.Minute)
	sweep(t, &flakyStore{Store: local, lost: []string{key}})
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
	sweep(t, &flakyStore{Store: local, lost: []string{rawKey("claude", "projects/"+slug+"/"+claudeSID+".jsonl")}})
	sweep(t, local)
	if !bytes.Equal(readBlob(t, e, "blobs/"+raw.SHA256(old)+".zst"), old) {
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
		{"appendLanded", false, true, ""},
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
			}
			testenv.Backdate(t, path, time.Hour)
			flaky := &flakyStore{Store: local, fail: []string{"plans/p.md.zst"}}
			if c.lost {
				flaky = &flakyStore{Store: local, lost: []string{"plans/p.md.zst"}}
			}
			sweep(t, flaky)
			testenv.Write(t, path, "X\n")
			testenv.Backdate(t, path, time.Hour)
			s := sweep(t, local)

			for _, k := range e.BucketKeys() {
				if sha, ok := strings.CutPrefix(k, "blobs/"); ok && raw.SHA256(readBlob(t, e, k))+".zst" != sha {
					t.Errorf("%s holds other bytes than it is named for", k)
				}
			}
			index := "raw-versions/agent=claude/machine=testbox/plans/p.md.json"
			if c.kept == "" {
				if e.HasKey(index) || s.Errors != 1 {
					t.Errorf("kept a version of bytes it no longer had, or didn't say so: %d errors", s.Errors)
				}
				return
			}
			versions := list(e.ReadJSON(index)["versions"])
			if s.Errors != 0 || len(versions) != 1 {
				t.Fatalf("%d errors %v, versions %v", s.Errors, s.ErrorMessages, versions)
			}
			if got := readPieces(t, e, versions[0].(map[string]any)["pieces"]); string(got) != c.kept {
				t.Errorf("version holds %q, want %q", got, c.kept)
			}
		})
	}
}

func TestRewriteWhoseOldObjectIsGoneStillUploads(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	key := rawKey("claude", "plans/brave-plan.md")
	if err := os.Remove(filepath.Join(e.Bucket, key)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.Claude, "plans", "brave-plan.md")
	testenv.Write(t, path, "# new plan\n")
	testenv.Backdate(t, path, 2*time.Minute)
	if code, _ := run(t, "sync", "--quiet"); code == 0 {
		t.Error("losing the earlier bytes went unreported")
	}
	if msgs := fmt.Sprint(lastRunSummary(t, e)["error_messages"]); !strings.Contains(msgs, key) {
		t.Errorf("error messages %s don't name %s", msgs, key)
	}
	if string(readBlob(t, e, key)) != "# new plan\n" {
		t.Error("rewrite not uploaded")
	}
	if hasPrefix(e.BucketKeys(), "blobs/", "raw-versions/") {
		t.Error("kept a version of bytes it no longer had")
	}
}

func TestLargeFileBeingWrittenHoldsBackItsLastLine(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64, 16, 10*time.Minute)
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	complete := readFile(t, path)
	testenv.Append(t, path, `{"type":"message","id":"half`)
	mustRun(t, "sync", "--quiet")
	key := rawKey("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
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
			key := rawKey("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
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
	key := rawKey("claude", "plans/brave-plan.md")
	old := readBlob(t, e, key)
	path := filepath.Join(e.Claude, "plans", "brave-plan.md")
	testenv.Write(t, path, "# new plan\n")
	testenv.Backdate(t, path, 2*time.Minute)
	if s := sweep(t, &flakyStore{Store: local, fail: []string{"blobs/"}}); s.Errors != 1 {
		t.Errorf("errors = %v", s.ErrorMessages)
	}
	if !bytes.Equal(readBlob(t, e, key), old) {
		t.Fatal("rewrite uploaded before the bytes it replaces were kept")
	}
	if s := sweep(t, local); s.Errors != 0 || s.Versions != 1 {
		t.Errorf("retry: %+v", s)
	}
	if string(readBlob(t, e, key)) != "# new plan\n" || !hasPrefix(e.BucketKeys(), "blobs/") {
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
	s := sweepFound(ctx, t, store.NewLocal(e.Bucket), func([]adapters.SessionFile) { cancel() })
	if keys := e.BucketKeys(); s.Uploaded != 0 || len(keys) != 0 {
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
	key := rawKey("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
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
	if e.HasKey(rawKey("claude", "settings.local.json")) {
		t.Error("uploaded a config file it couldn't check")
	}
	if !e.HasKey(rawKey("claude", "settings.json")) {
		t.Error("one unparseable file stopped the others")
	}
}
