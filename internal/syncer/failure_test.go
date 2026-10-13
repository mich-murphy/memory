package syncer_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/syncer"
	"github.com/mich-murphy/memory/internal/testenv"
)

var errInjected = errors.New("injected failure")

// flakyStore fails writes to keys containing any of fail.
type flakyStore struct {
	store.Store
	fail []string
}

func (f *flakyStore) broken(key string) bool {
	for _, s := range f.fail {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}

func (f *flakyStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if f.broken(key) {
		return errInjected
	}
	return f.Store.Put(ctx, key, body, meta)
}

func (f *flakyStore) PutFile(ctx context.Context, key, path string, meta map[string]string) error {
	if f.broken(key) {
		return errInjected
	}
	return f.Store.PutFile(ctx, key, path, meta)
}

func (f *flakyStore) Copy(ctx context.Context, src, dst string) (bool, error) {
	if f.broken(dst) {
		return false, errInjected
	}
	return f.Store.Copy(ctx, src, dst)
}

// sweep runs one sweep through the syncer API with st as the bucket.
func sweep(t *testing.T, st store.Store) *syncer.Summary {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy := syncer.New(cfg, st, s, adapters.All(cfg.Home), syncer.Options{Trigger: "sweep", Snapshots: true, Logger: slog.New(slog.DiscardHandler)})
	files, err := sy.SweepFiles(adapters.Names)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := sy.Run(t.Context(), files)
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func TestAFailedUploadIsCountedAndRetriedNextRun(t *testing.T) {
	e := testenv.New(t)
	local := store.NewLocal(e.Bucket)
	key := rawKey("claude", "plans/brave-plan.md")
	first := sweep(t, &flakyStore{Store: local, fail: []string{"plans/brave-plan.md", "manifests/agent=pi"}})
	if first.Errors != 2 || len(first.ErrorMessages) != 2 {
		t.Errorf("errors = %d %v", first.Errors, first.ErrorMessages)
	}
	if e.HasKey(key) {
		t.Fatal("failed upload landed anyway")
	}
	if !e.HasKey(rawKey("claude", "history.jsonl")) {
		t.Error("one failure stopped the other uploads")
	}
	second := sweep(t, local)
	if second.Errors != 0 || !e.HasKey(key) || !e.HasKey("manifests/agent=pi/machine=testbox/"+piKey+".json") {
		t.Errorf("retry: errors %v, keys %v", second.ErrorMessages, e.BucketKeys())
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
	mustRun(t, "sync", "--quiet")
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
			if s := sweep(t, store.NewLocal(e.Bucket)); s.Skipped == 0 || s.Errors != 0 {
				t.Errorf("summary = %+v", s)
			}
			key := rawKey("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
			if e.HasKey(key) {
				t.Error("a partial first line was uploaded")
			}
		})
	}
}

func TestSnapshotsAreRecheckedAndOnlyAddedWhenSomethingChanged(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64<<20, 1<<20, 0)
	session := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	turn := func() {
		testenv.Append(t, session, testenv.JSONL(t, map[string]any{"type": "user"}))
		mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", session)
	}
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", session)
	turn()
	if n := len(list(e.Manifest("claude", claudeKey)["snapshots"])); n != 1 {
		t.Fatalf("%d snapshots after an unchanged recheck", n)
	}
	testenv.Write(t, filepath.Join(e.Project, "NEW.md"), "x\n")
	testenv.Git(t, e.Project, "add", "-A")
	testenv.Git(t, e.Project, "commit", "-q", "-m", "second")
	testenv.Write(t, filepath.Join(e.Claude, "CLAUDE.md"), "user rules, edited\n")
	turn()
	snaps := list(e.Manifest("claude", claudeKey)["snapshots"])
	if len(snaps) != 2 {
		t.Fatalf("%d snapshots after a commit and an edit", len(snaps))
	}
	a, b := snaps[0].(map[string]any), snaps[1].(map[string]any)
	if a["id"] == b["id"] || a["git"].(map[string]any)["head"] == b["git"].(map[string]any)["head"] {
		t.Errorf("second snapshot didn't change: %v vs %v", a, b)
	}
	g := b["git"].(map[string]any)
	log := gunzip(t, filepath.Join(e.Bucket, "git", g["repo_id"].(string), "log", g["head"].(string)+".txt.gz"))
	if !strings.Contains(log, "second") || strings.Contains(log, "first") {
		t.Errorf("second export should carry only the new commit:\n%s", log)
	}
}

func TestSyncWaitsForTheLockThenGivesUp(t *testing.T) {
	e := testenv.New(t)
	unlock, err := state.Lock(t.Context(), e.State, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := state.Lock(t.Context(), e.State, 300*time.Millisecond); !errors.Is(err, state.ErrLocked) {
		t.Errorf("second lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, err := state.Lock(ctx, e.State, time.Minute); err == nil {
		t.Error("lock taken while held")
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
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	key := rawKey("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("large file not uploaded on retry")
	}
}

func TestAConfigFileThatCannotBeCheckedForSecretsIsNotUploaded(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Claude, "settings.local.json"), `{"env": {"API_KEY": "sk-half-written`)
	summary := sweep(t, store.NewLocal(e.Bucket))
	if summary.Errors != 1 || !strings.Contains(strings.Join(summary.ErrorMessages, " "), "settings.local.json") {
		t.Errorf("errors = %d %v", summary.Errors, summary.ErrorMessages)
	}
	if e.HasKey(rawKey("claude", "settings.local.json")) {
		t.Error("uploaded a config file it couldn't check")
	}
	if !e.HasKey(rawKey("claude", "settings.json")) {
		t.Error("one unparseable file stopped the others")
	}
}
