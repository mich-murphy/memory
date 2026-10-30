package syncer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestArchiveColdRecoveryRetainsLaunchAfterOlderManifest(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	path := filepath.Join(home, ".claude", "projects", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(cfg.LocalStore)
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	first := launch.Envelope{Schema: 1, ID: "review-first", Agent: "claude", Path: path, RecordedAt: 100, Payload: map[string]any{}}
	second := launch.Envelope{Schema: 1, ID: "review-second", Agent: "claude", Path: path, RecordedAt: 200, Payload: map[string]any{}}
	if err := sy.RecordEnvelope(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	sum, err := sy.Run(t.Context(), nil)
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	// New invocation publishes a durable launch then crashes before the next manifest.
	sy = New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.RecordEnvelope(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	p, _, err := sy.current("launch", "claude", second.ID)
	if err != nil || p == nil {
		t.Fatalf("launch commit = %v, %v", p, err)
	}
	ref := adapters.SessionRef{Agent: "claude", Key: p.Launch.SessionKey}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy = New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err := db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil || len(sess.Launches) != 2 {
		t.Fatalf("cold recovered session = %+v, want two durable launches", sess)
	}
}

type reviewDelayedList struct {
	store.Store
	armed        bool
	deadlineSeen bool
}

func (b *reviewDelayedList) List(ctx context.Context, prefix, token string, limit int) (store.Page, error) {
	if b.armed && strings.Contains(prefix, "/records/file/") {
		_, b.deadlineSeen = ctx.Deadline()
		select {
		case <-ctx.Done():
			return store.Page{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return b.Store.List(ctx, prefix, token, limit)
}

func TestArchiveRunHonorsItsOwnDeadlineAfterBackgroundRecovery(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	bucket := &reviewDelayedList{Store: store.NewLocal(cfg.LocalStore)}
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "aux")
	if err := os.WriteFile(path, []byte("A"), 0o600); err != nil {
		t.Fatal(err)
	}
	bucket.armed = true
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = sy.Run(ctx, []adapters.SessionFile{{Agent: "claude", Root: home, Path: path, Key: "aux", Role: adapters.RoleAux}})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v, want deadline exceeded", err)
	}
	if !bucket.deadlineSeen || elapsed > 150*time.Millisecond {
		t.Fatalf("Run metadata deadline = %v, elapsed = %v; want own short deadline propagated", bucket.deadlineSeen, elapsed)
	}
}

type reviewCrashStore struct {
	store.Store
	phase string
	after bool
	dead  bool
	fired bool
}

var errReviewCrash = errors.New("review injected process death")

func (b *reviewCrashStore) phaseFor(key string, body []byte) string {
	if strings.Contains(key, "/records/session/") && strings.Contains(key, "/plans/") {
		return "manifestPlan"
	}
	if strings.Contains(key, "/records/session/") && strings.Contains(key, "/commits/") {
		return "manifestCommit"
	}
	if strings.Contains(key, "/records/launch/") && strings.Contains(key, "/plans/") {
		return "launchPlan"
	}
	if strings.Contains(key, "/records/launch/") && strings.Contains(key, "/commits/") {
		return "launchCommit"
	}
	if strings.HasPrefix(key, "v3/blobs/plain/") {
		return "snapshotBlob"
	}
	if strings.HasPrefix(key, "v3/snapshots/sets/") {
		return "snapshotSet"
	}
	if strings.Contains(key, "/records/file/") && strings.Contains(key, "/plans/") {
		return "plan"
	}
	if strings.HasPrefix(key, "v3/blobs/zstd/") {
		return "dependency"
	}
	if strings.Contains(key, "/records/file/") && strings.Contains(key, "/commits/") {
		return "commit"
	}
	if strings.HasSuffix(key, "/checkpoint.json") {
		if strings.Contains(string(body), `"complete":true`) {
			return "completeCheckpoint"
		}
		return "dirtyCheckpoint"
	}
	return ""
}

func (b *reviewCrashStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	if b.dead {
		return errReviewCrash
	}
	if b.phaseFor(key, body) != b.phase {
		return b.Store.Put(ctx, key, body, meta)
	}
	b.fired = true
	if b.after {
		if err := b.Store.Put(ctx, key, body, meta); err != nil {
			return err
		}
	}
	b.dead = true
	return errReviewCrash
}

func (b *reviewCrashStore) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if b.dead {
		return nil, errReviewCrash
	}
	return b.Store.OpenReader(ctx, key)
}

func (b *reviewCrashStore) Head(ctx context.Context, key string) (store.Object, error) {
	if b.dead {
		return store.Object{}, errReviewCrash
	}
	return b.Store.Head(ctx, key)
}

func TestArchiveCrashAtEveryFilePublicationBoundary(t *testing.T) {
	t.Parallel() // hermetic: private temp dirs and stores, no env or package globals
	for _, phase := range []string{"dirtyCheckpoint", "plan", "dependency", "commit", "completeCheckpoint"} {
		for _, after := range []bool{false, true} {
			name := phase + "Before"
			if after {
				name = phase + "After"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				home := t.TempDir()
				cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
				path := filepath.Join(home, "aux")
				if err := os.WriteFile(path, []byte("archived bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
				underlying := store.NewLocal(cfg.LocalStore)
				bucket := &reviewCrashStore{Store: underlying, phase: phase, after: after}
				db, err := state.Open(t.Context(), cfg.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				sy := New(cfg, bucket, db, adapters.All(home), Options{})
				sum, runErr := sy.Run(t.Context(), []adapters.SessionFile{{Agent: "claude", Root: home, Path: path, Key: "aux", Role: adapters.RoleAux}})
				if !bucket.fired {
					t.Fatalf("fault not triggered: summary %+v, error %v", sum, runErr)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(cfg.StateDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				db, err = state.Open(t.Context(), cfg.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}()
				sy = New(cfg, underlying, db, adapters.All(home), Options{})
				if err := sy.Recover(t.Context()); err != nil {
					t.Fatal(err)
				}
				var restored bytes.Buffer
				err = sy.Restore(t.Context(), "claude", "aux", "", &restored)
				wantContent := phase == "completeCheckpoint" || phase == "commit" || phase == "dependency" && after
				if wantContent {
					if err != nil || restored.String() != "archived bytes" {
						t.Fatalf("Restore = %q, %v; want archived bytes", restored.String(), err)
					}
				} else if err == nil {
					t.Fatalf("Restore = %q, want absent because capture bytes never durable", restored.String())
				}
			})
		}
	}
}

type reviewLostACK struct {
	store.Store
	phase string
	fired bool
}

func (b *reviewLostACK) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	err := b.Store.Put(ctx, key, body, meta)
	if err != nil {
		return err
	}
	classifier := reviewCrashStore{}
	if !b.fired && classifier.phaseFor(key, body) == b.phase {
		b.fired = true
		return errReviewCrash
	}
	return nil
}

func TestArchiveLostACKAtEveryFilePublicationBoundary(t *testing.T) {
	t.Parallel() // hermetic: private temp dirs and stores, no env or package globals
	for _, phase := range []string{"dirtyCheckpoint", "plan", "dependency", "commit", "completeCheckpoint"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
			path := filepath.Join(home, "aux")
			if err := os.WriteFile(path, []byte("archived bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			underlying := store.NewLocal(cfg.LocalStore)
			bucket := &reviewLostACK{Store: underlying, phase: phase}
			db, err := state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			sy := New(cfg, bucket, db, adapters.All(home), Options{})
			sum, err := sy.Run(t.Context(), []adapters.SessionFile{{Agent: "claude", Root: home, Path: path, Key: "aux", Role: adapters.RoleAux}})
			if err != nil || sum.Errors != 0 || !bucket.fired {
				t.Fatalf("Run = %+v, %v; fired %v", sum, err, bucket.fired)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(cfg.StateDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			db, err = state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			sy = New(cfg, underlying, db, adapters.All(home), Options{})
			var restored bytes.Buffer
			if err := sy.Restore(t.Context(), "claude", "aux", "", &restored); err != nil {
				t.Fatal(err)
			}
			if restored.String() != "archived bytes" {
				t.Fatalf("Restore = %q", restored.String())
			}
		})
	}
}

func TestArchiveSharedPathStatCacheCannotSkipDifferentAgentStream(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	path := filepath.Join(home, "history.jsonl")
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	bucket := store.NewLocal(cfg.LocalStore)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(agent string) {
		t.Helper()
		sy := New(cfg, bucket, db, adapters.All(home), Options{})
		sum, err := sy.Run(t.Context(), []adapters.SessionFile{{Agent: agent, Root: home, Path: path, Key: "history.jsonl", Role: adapters.RoleHistory}})
		if err != nil || sum.Errors != 0 {
			t.Fatalf("Run(%s) = %+v, %v", agent, sum, err)
		}
	}
	write("A\n")
	run("claude")
	run("codex")
	write("B-more\n")
	run("claude")
	run("codex")
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	var got bytes.Buffer
	if err := sy.Restore(t.Context(), "codex", "history.jsonl", "", &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != "B-more\n" {
		t.Fatalf("codex current = %q, want latest shared source bytes B-more", got.String())
	}
}

func TestArchiveRunIsExplicitlyOneShot(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy := New(cfg, store.NewLocal(cfg.LocalStore), db, adapters.All(home), Options{})
	if _, err := sy.Run(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sy.Run(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "already finished") {
		t.Fatalf("second Run = %v, want explicit lifecycle rejection", err)
	}
}

func TestArchiveColdRecoveryRejectsBrokenRemoteEvidence(t *testing.T) {
	for _, damage := range []string{"none", "missingBlob", "corruptBlob", "missingParent", "branch", "unknownRecord"} {
		t.Run(damage, func(t *testing.T) {
			home := t.TempDir()
			cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
			db, err := state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			bucket := store.NewLocal(cfg.LocalStore)
			path := filepath.Join(home, "aux")
			var firstRecord *state.Record
			var sy *Syncer
			for i, body := range []string{"A", "B-more"} {
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				sy = New(cfg, bucket, db, adapters.All(home), Options{})
				sum, err := sy.Run(t.Context(), []adapters.SessionFile{{Agent: "claude", Root: home, Path: path, Key: "aux", Role: adapters.RoleAux}})
				if err != nil || sum.Errors != 0 {
					t.Fatalf("Run = %+v, %v", sum, err)
				}
				if i == 0 {
					_, firstRecord, err = sy.current("file", "claude", "aux")
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			current, currentRecord, err := sy.current("file", "claude", "aux")
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "none":
			case "missingBlob":
				if err := bucket.Delete(t.Context(), current.File.Pieces[len(current.File.Pieces)-1].Object); err != nil {
					t.Fatal(err)
				}
			case "corruptBlob":
				key := current.File.Pieces[len(current.File.Pieces)-1].Object
				body, err := bucket.Get(t.Context(), key)
				if err != nil {
					t.Fatal(err)
				}
				body[len(body)/2] ^= 1
				if err := bucket.Put(t.Context(), key, body, nil); err != nil {
					t.Fatal(err)
				}
			case "missingParent":
				p, err := decodePublication(firstRecord.Body)
				if err != nil {
					t.Fatal(err)
				}
				for _, committed := range []bool{false, true} {
					if err := bucket.Delete(t.Context(), sy.publicationKey(p, committed, firstRecord.ID)); err != nil {
						t.Fatal(err)
					}
				}
			case "branch":
				p, err := decodePublication(currentRecord.Body)
				if err != nil {
					t.Fatal(err)
				}
				p.File.ObservedAt++
				body, id, err := canonicalPublication(p)
				if err != nil {
					t.Fatal(err)
				}
				if err := bucket.Put(t.Context(), sy.publicationKey(p, true, id), body, map[string]string{"sha256": id}); err != nil {
					t.Fatal(err)
				}
			case "unknownRecord":
				if err := bucket.Put(t.Context(), sy.machinePrefix()+"records/unknown.json", []byte(`{"schema":99}`), nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(cfg.StateDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			db, err = state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			sy = New(cfg, bucket, db, adapters.All(home), Options{})
			err = sy.Recover(t.Context())
			if damage != "none" {
				if err == nil {
					t.Fatal("Recover accepted broken evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			if err := sy.Restore(t.Context(), "claude", "aux", "", &got); err != nil {
				t.Fatal(err)
			}
			if got.String() != "B-more" {
				t.Fatalf("Restore = %q", got.String())
			}
		})
	}
}

func TestArchiveColdLaunchReplayPreservesSnapshotTransitions(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	root := filepath.Join(home, ".claude")
	path := filepath.Join(root, "projects", "project", "session.jsonl")
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(cfg.LocalStore)
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	var want []string
	var ref adapters.SessionRef
	for i, body := range []string{"A", "B", "A"} {
		if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		sy.clock = func() time.Time { return time.Unix(int64(1000+i), 0) }
		e := launch.Envelope{Schema: 1, ID: fmt.Sprintf("event-%d", i+1), Agent: "claude", Path: path, RecordedAt: float64(100 + i), Payload: map[string]any{"cwd": cwd}}
		if err := sy.RecordEnvelope(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		p, _, err := sy.current("launch", "claude", e.ID)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, p.Launch.Snapshot.ID)
		ref = adapters.SessionRef{Agent: "claude", Key: p.Launch.SessionKey}
	}
	sess, err := db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Snapshots) != 3 {
		t.Fatalf("baseline snapshots = %d, want 3", len(sess.Snapshots))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy = New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err = db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, snap := range sess.Snapshots {
		got = append(got, snap.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cold recovered snapshot transitions = %v, want %v", got, want)
	}
}

func TestArchiveBindingRejectsDestinationMachineAndStoreReplacement(t *testing.T) {
	for _, change := range []string{"destination", "machine", "storeIdentity"} {
		t.Run(change, func(t *testing.T) {
			home := t.TempDir()
			cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
			db, err := state.Open(t.Context(), cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			bucket := store.NewLocal(cfg.LocalStore)
			sy := New(cfg, bucket, db, adapters.All(home), Options{})
			if _, err := sy.Run(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			changed := *cfg
			switch change {
			case "destination":
				changed.LocalStore = t.TempDir()
			case "machine":
				changed.MachineID = "other-machine"
			case "storeIdentity":
				if err := bucket.Put(t.Context(), "v3/store.json", []byte(`{"schema":3,"id":"replacement"}`), nil); err != nil {
					t.Fatal(err)
				}
			}
			sy = New(&changed, store.NewLocal(changed.LocalStore), db, adapters.All(home), Options{})
			if err := sy.Recover(t.Context()); err == nil {
				t.Fatal("Recover accepted incompatible binding")
			}
		})
	}
}

func TestArchiveMachineDestinationLockIndependentOfCache(t *testing.T) {
	cfg := &config.Config{Home: t.TempDir(), StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	other := *cfg
	other.StateDir = t.TempDir()
	first, err := LockDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LockDir(&other)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("LockDir = %q and %q, want identical", first, second)
	}
	unlock, err := state.Lock(t.Context(), first, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	release, err := state.Lock(t.Context(), second, 20*time.Millisecond)
	if err == nil {
		release()
		t.Fatal("different cache bypassed existing machine destination lock")
	}
	if !errors.Is(err, state.ErrLocked) {
		t.Fatalf("Lock = %v, want locked", err)
	}
}

func TestArchiveRemoteExclusionRestoresBeforeRawExportsAndSnapshots(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	private := filepath.Join(root, "projects", "project", "private.jsonl")
	allowed := filepath.Join(root, "projects", "project", "allowed.jsonl")
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(private), 0o700); err != nil {
		t.Fatal(err)
	}
	const sentinel = "REVIEW_PRIVATE_SENTINEL_NEVER_CAPTURE"
	for path, body := range map[string]string{private: sentinel + "\n", filepath.Join(root, "history.jsonl"): sentinel + "\n", filepath.Join(home, ".claude.json"): `{"unrecognised":"` + sentinel + `"}`, filepath.Join(root, "CLAUDE.md"): sentinel, allowed: fmt.Sprintf("{\"sessionId\":\"allowed\",\"cwd\":%q}\n", cwd)} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := exclusion.RecordPath(cfg.StateDir, adapters.NewClaude(home), private); err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(cfg.LocalStore)
	for pass := range 2 {
		db, err := state.Open(t.Context(), cfg.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		sy := New(cfg, bucket, db, adapters.All(home), Options{})
		files, err := sy.SweepFiles(t.Context(), []string{"claude"})
		if err != nil {
			t.Fatal(err)
		}
		for _, sf := range files {
			if sf.Path == private || sf.Key == "history.jsonl" || sf.Key == "claude.json" {
				t.Fatalf("pass %d selected excluded source %+v", pass, sf)
			}
		}
		sum, err := sy.Run(t.Context(), files)
		if err != nil || sum.Errors != 0 {
			t.Fatalf("Run = %+v, %v", sum, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if pass == 0 {
			if err := os.RemoveAll(cfg.StateDir); err != nil {
				t.Fatal(err)
			}
		}
	}
	page, err := bucket.List(t.Context(), "v3/", "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range page.Objects {
		body, err := bucket.Get(t.Context(), obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(obj.Key, ".zst") {
			body, err = raw.Decompress(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		if bytes.Contains(body, []byte(sentinel)) {
			t.Fatalf("private sentinel in archived object %s", obj.Key)
		}
	}
}

func TestArchiveFirstV3BindingDiscardsLegacyProjectionsNotRemoteHistory(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	ref := adapters.SessionRef{Agent: "claude", Key: "project/session"}
	old := &state.Session{Info: map[string]any{"cwd": home, "source_root": filepath.Join(home, ".claude")}, FirstSeen: 1, Snapshots: []state.SnapshotEntry{{ID: "legacy-not-v3-set", Timing: "pre_session", TakenAt: 1}}}
	const legacyKey = "raw/agent=claude/machine=review-machine/old.jsonl.zst"
	if err := db.Atomic(func() error {
		if err := db.PutSession(ref, old); err != nil {
			return err
		}
		if err := db.MarkManifestPending(ref); err != nil {
			return err
		}
		return db.RecordUpload(legacyKey, "legacy-checksum", 10)
	}); err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(cfg.LocalStore)
	sentinel := []byte("legacy remote history remains untouched")
	if err := bucket.Put(t.Context(), legacyKey, sentinel, nil); err != nil {
		t.Fatal(err)
	}
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err := db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	if sess != nil {
		t.Fatalf("legacy session reused: %+v", sess)
	}
	upload, err := db.Uploaded(legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	if upload != nil {
		t.Fatalf("legacy upload reused: %+v", upload)
	}
	pending, err := db.PendingManifests()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("legacy pending refs = %v", pending)
	}
	sum, err := sy.Run(t.Context(), nil)
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run after transition = %+v, %v", sum, err)
	}
	got, err := bucket.Get(t.Context(), legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatal("legacy remote history changed")
	}
}

func TestArchiveLaunchSnapshotCrashAndLostACKBoundaries(t *testing.T) {
	for _, phase := range []string{"launchPlan", "snapshotBlob", "snapshotSet", "launchCommit"} {
		for _, mode := range []string{"before", "after", "lostACK"} {
			t.Run(phase+"-"+mode, func(t *testing.T) {
				home := t.TempDir()
				cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
				t.Setenv("HOME", home)
				t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
				t.Setenv("CLAUDE_CONFIG_DIR", "")
				root := filepath.Join(home, ".claude")
				path := filepath.Join(root, "projects", "project", "session.jsonl")
				cwd := filepath.Join(home, "project")
				if err := os.MkdirAll(cwd, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("review harness captured bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
				underlying := store.NewLocal(cfg.LocalStore)
				crashed := &reviewCrashStore{Store: underlying, phase: phase, after: mode == "after"}
				lost := &reviewLostACK{Store: underlying, phase: phase}
				var bucket store.Store = crashed
				if mode == "lostACK" {
					bucket = lost
				}
				db, err := state.Open(t.Context(), cfg.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				sy := New(cfg, bucket, db, adapters.All(home), Options{})
				e := launch.Envelope{Schema: 1, ID: "event", Agent: "claude", Path: path, RecordedAt: 100, Payload: map[string]any{"cwd": cwd}}
				launchErr := sy.RecordEnvelope(t.Context(), e)
				if mode == "lostACK" {
					if !lost.fired || launchErr != nil {
						t.Fatalf("lostACK RecordEnvelope = %v, fired %v", launchErr, lost.fired)
					}
				} else if !crashed.fired {
					t.Fatalf("crash fault not fired; RecordEnvelope = %v", launchErr)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(cfg.StateDir); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
				db, err = state.Open(t.Context(), cfg.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}()
				sy = New(cfg, underlying, db, adapters.All(home), Options{})
				if err := sy.Recover(t.Context()); err != nil {
					t.Fatal(err)
				}
				p, _, err := sy.current("launch", "claude", e.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantLaunch := mode == "lostACK" || phase == "launchCommit" || phase == "snapshotSet" && mode == "after"
				if (p != nil) != wantLaunch {
					t.Fatalf("recovered launch present = %v, want %v", p != nil, wantLaunch)
				}
				if p != nil {
					if p.Launch.Snapshot.ID == "" {
						t.Fatal("durable launch lost snapshot")
					}
					sess, err := db.Session(adapters.SessionRef{Agent: "claude", Key: p.Launch.SessionKey})
					if err != nil || sess == nil || len(sess.Launches) != 1 {
						t.Fatalf("recovered session = %+v, %v", sess, err)
					}
				}
			})
		}
	}
}

func TestArchiveSnapshotTransitionsFollowCaptureRatherThanEventTime(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	root := filepath.Join(home, ".claude")
	path := filepath.Join(root, "projects", "project", "session.jsonl")
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(cfg.LocalStore)
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	var want []string
	var ref adapters.SessionRef
	for i, body := range []string{"A", "B", "A"} {
		if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		sy.clock = func() time.Time { return time.Unix(int64(1000+i), 0) }
		e := launch.Envelope{Schema: 1, ID: fmt.Sprintf("event-%d", i+1), Agent: "claude", Path: path, RecordedAt: []float64{100, 300, 200}[i], Payload: map[string]any{"cwd": cwd}}
		if err := sy.RecordEnvelope(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		p, _, err := sy.current("launch", "claude", e.ID)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, p.Launch.Snapshot.ID)
		ref = adapters.SessionRef{Agent: "claude", Key: p.Launch.SessionKey}
	}
	sess, err := db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Snapshots) != 3 {
		t.Fatalf("baseline snapshots = %d, want 3", len(sess.Snapshots))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy = New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err = db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, snap := range sess.Snapshots {
		got = append(got, snap.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cold recovered snapshot transitions = %v, want %v", got, want)
	}
}

func TestArchiveRecoveredLateLaunchMakesManifestPending(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	path := filepath.Join(home, ".claude", "projects", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(cfg.LocalStore)
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	sy := New(cfg, bucket, db, adapters.All(home), Options{})
	first := launch.Envelope{Schema: 1, ID: "review-first", Agent: "claude", Path: path, RecordedAt: 100, Payload: map[string]any{}}
	second := launch.Envelope{Schema: 1, ID: "review-second", Agent: "claude", Path: path, RecordedAt: 200, Payload: map[string]any{}}
	if err := sy.RecordEnvelope(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	sum, err := sy.Run(t.Context(), nil)
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	// New invocation publishes a durable launch then crashes before the next manifest.
	sy = New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.RecordEnvelope(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	p, _, err := sy.current("launch", "claude", second.ID)
	if err != nil || p == nil {
		t.Fatalf("launch commit = %v, %v", p, err)
	}
	ref := adapters.SessionRef{Agent: "claude", Key: p.Launch.SessionKey}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy = New(cfg, bucket, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err := db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil || len(sess.Launches) != 2 {
		t.Fatalf("cold recovered session = %+v, want two durable launches", sess)
	}
	sum, err = sy.Run(t.Context(), nil)
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run after recovery = %+v, %v", sum, err)
	}
	latest, _, err := sy.current("session", "claude", ref.Key)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || len(latest.Manifest.LaunchRecords) != 2 {
		t.Fatalf("post-recovery current manifest = %+v, want both durable launches", latest)
	}
}

func TestColdRecoveryKeepsAReferencedLaunchCurrentButALateOnePending(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, LocalStore: t.TempDir(), MachineID: "review-machine"}
	path := filepath.Join(home, ".claude", "projects", "project", "session.jsonl")
	testenv.Write(t, path, "{}\n")
	bucket := store.NewLocal(cfg.LocalStore)
	fresh := func() (*Syncer, *state.State) {
		t.Helper()
		cfg := *cfg
		cfg.StateDir = t.TempDir()
		db, err := state.Open(t.Context(), cfg.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		})
		return New(&cfg, bucket, db, adapters.All(home), Options{}), db
	}
	pending := func() []adapters.SessionRef {
		t.Helper()
		sy, db := fresh()
		if err := sy.Recover(t.Context()); err != nil {
			t.Fatal(err)
		}
		refs, err := db.PendingManifests()
		if err != nil {
			t.Fatal(err)
		}
		return refs
	}
	sy, _ := fresh()
	if err := sy.RecordEnvelope(t.Context(), launch.Envelope{Schema: 1, ID: "first", Agent: "claude", Path: path, RecordedAt: 100, Payload: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if sum, err := sy.Run(t.Context(), nil); err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	if refs := pending(); len(refs) != 0 {
		t.Fatalf("pending after recovering a current session = %v, want none", refs)
	}
	sy, _ = fresh()
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sy.RecordEnvelope(t.Context(), launch.Envelope{Schema: 1, ID: "late", Agent: "claude", Path: path, RecordedAt: 200, Payload: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if refs := pending(); len(refs) != 1 || refs[0].Agent != "claude" {
		t.Fatalf("pending after recovering a late launch = %v, want the session", refs)
	}
}

func TestArchiveRecoveredNewFileMakesManifestPending(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	root := filepath.Join(home, ".claude")
	path := filepath.Join(root, "projects", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	sf := adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: "projects/project/session.jsonl", SessionKey: "project/session", Role: adapters.RoleMain}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	underlying := store.NewLocal(cfg.LocalStore)
	if err := os.WriteFile(path, []byte("A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sy := New(cfg, underlying, db, adapters.All(home), Options{})
	sum, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
	if err != nil || sum.Errors != 0 {
		t.Fatalf("initial Run = %+v, %v", sum, err)
	}
	child := sf
	child.Path, child.Key, child.Role = filepath.Join(root, "projects", "project", "session", "subagents", "agent-x.jsonl"), "projects/project/session/subagents/agent-x.jsonl", adapters.RoleSubagent
	if err := os.MkdirAll(filepath.Dir(child.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child.Path, []byte("B-longer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	crashed := &reviewCrashStore{Store: underlying, phase: "manifestPlan"}
	sy = New(cfg, crashed, db, adapters.All(home), Options{})
	_, _ = sy.Run(t.Context(), []adapters.SessionFile{child})
	if !crashed.fired {
		t.Fatal("did not crash after a new file's commit before its manifest")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, child.Path} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy = New(cfg, underlying, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := sy.Restore(t.Context(), "claude", child.Key, "", &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != "B-longer\n" {
		t.Fatalf("raw current = %q", got.String())
	}
	sum, err = sy.Run(t.Context(), nil)
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run after recovery = %+v, %v", sum, err)
	}
	latest, _, err := sy.current("session", "claude", sf.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || len(latest.Manifest.Transcripts) != 2 || latest.Manifest.Transcripts[1].Key != child.Key {
		t.Fatalf("current manifest did not list the recovered new file: %+v", latest)
	}
}

type reviewPagedStore struct {
	store.Store
	pages    int
	failPage int
	pageSize int
}

func (b *reviewPagedStore) List(ctx context.Context, prefix, token string, limit int) (store.Page, error) {
	if strings.HasSuffix(prefix, "/records/") {
		b.pages++
		if b.pages == b.failPage {
			return store.Page{}, errors.New("review interrupted metadata listing")
		}
		limit = min(limit, b.pageSize)
	}
	return b.Store.List(ctx, prefix, token, limit)
}

func TestArchivePaginatedColdRecoveryResumesAfterMidPageFailure(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	underlying := store.NewLocal(cfg.LocalStore)
	var files []adapters.SessionFile
	for i := range 11 {
		key := fmt.Sprintf("aux-%02d", i)
		path := filepath.Join(home, key)
		if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, adapters.SessionFile{Agent: "claude", Root: home, Path: path, Key: key, Role: adapters.RoleAux})
	}
	sy := New(cfg, underlying, db, adapters.All(home), Options{})
	sum, err := sy.Run(t.Context(), files)
	if err != nil || sum.Errors != 0 {
		t.Fatalf("initial Run = %+v, %v", sum, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	for _, sf := range files {
		if err := os.Remove(sf.Path); err != nil {
			t.Fatal(err)
		}
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	paged := &reviewPagedStore{Store: underlying, failPage: 3, pageSize: 2}
	sy = New(cfg, paged, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err == nil || !strings.Contains(err.Error(), "interrupted metadata listing") {
		t.Fatalf("interrupted Recover = %v", err)
	}
	if paged.pages != 3 {
		t.Fatalf("interrupted listing pages = %d, want 3", paged.pages)
	}
	paged.pages = 0
	paged.failPage = 0
	sy = New(cfg, paged, db, adapters.All(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if paged.pages != 11 {
		t.Fatalf("complete metadata pages = %d, want 11 for 22 records at page size 2", paged.pages)
	}
	for _, sf := range files {
		var got bytes.Buffer
		if err := sy.Restore(t.Context(), sf.Agent, sf.Key, "", &got); err != nil {
			t.Fatal(err)
		}
		if got.String() != sf.Key {
			t.Fatalf("Restore(%s) = %q", sf.Key, got.String())
		}
	}
}

func TestArchiveColdLaunchReplayPreservesCurrentSessionCWD(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "review-machine"}
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	path := filepath.Join(root, "projects", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(cfg.LocalStore)
	sy := New(cfg, bucket, db, reviewAdapters(home), Options{})
	var ref adapters.SessionRef
	for i := range 2 {
		cwd := filepath.Join(home, fmt.Sprintf("cwd-%d", i))
		if err := os.MkdirAll(cwd, 0o700); err != nil {
			t.Fatal(err)
		}
		sy.clock = func() time.Time { return time.Unix(int64(1000+i), 0) }
		e := launch.Envelope{Schema: 1, ID: fmt.Sprintf("event-%d", i+1), Agent: "claude", Path: path, RecordedAt: float64(100 + i), Payload: map[string]any{"cwd": cwd}}
		if err := sy.RecordEnvelope(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		p, _, err := sy.current("launch", "claude", e.ID)
		if err != nil {
			t.Fatal(err)
		}
		ref = adapters.SessionRef{Agent: "claude", Key: p.Launch.SessionKey}
	}
	sess, err := db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	want := sess.Info["cwd"]
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy = New(cfg, bucket, db, reviewAdapters(home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err = db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Info["cwd"] != want {
		t.Fatalf("cold recovered session cwd = %v, want precrash %v", sess.Info["cwd"], want)
	}
}

// Protocol fixtures omit host-wide managed configuration: all source inputs belong to this test's temporary home.
type reviewIsolatedAdapter struct{ adapters.Adapter }

func (reviewIsolatedAdapter) ManagedSettingsFiles() []string { return nil }
func reviewAdapters(home string) map[string]adapters.Adapter {
	out := adapters.All(home)
	for name, a := range out {
		out[name] = reviewIsolatedAdapter{Adapter: a}
	}
	return out
}

// A 0.3.0 collector kept post-hoc snapshots only in its copied manifests, so a cold upgrade from
// its bucket keeps them, republishes them under records/session/, and still has them once
// records/manifest/ is deleted.
func TestArchiveColdUpgradeFromCopiedManifestsKeepsSnapshots(t *testing.T) {
	bucketDir := t.TempDir()
	if err := os.CopyFS(bucketDir, os.DirFS("testdata/v0.3.0-bucket")); err != nil {
		t.Fatal(err)
	}
	bucket := store.NewLocal(bucketDir)
	open := func(t *testing.T, stateDir string) *Syncer {
		t.Helper()
		cfg := &config.Config{Home: t.TempDir(), StateDir: stateDir, LocalStore: bucketDir, MachineID: testenv.MachineID}
		db, err := state.Open(t.Context(), stateDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		})
		return New(cfg, bucket, db, adapters.All(cfg.Home), Options{Version: "test"})
	}
	sessions := []adapters.SessionRef{{Agent: "claude", Key: testenv.ClaudeKey}, {Agent: "codex", Key: testenv.CodexKey}, {Agent: "pi", Key: testenv.PiSessionKey}}
	// Each 0.3.0 manifest holds one post_hoc snapshot, which nothing else records.
	snapshots := func(t *testing.T, sy *Syncer, ref adapters.SessionRef) []string {
		t.Helper()
		sess, err := sy.db.Session(ref)
		if err != nil || sess == nil {
			t.Fatalf("%v session = %v, %v", ref, sess, err)
		}
		var ids []string
		for _, snap := range sess.Snapshots {
			if snap.Timing == "post_hoc" {
				ids = append(ids, snap.ID)
			}
		}
		return ids
	}

	stateDir := t.TempDir()
	sy := open(t, stateDir)
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	pending, err := sy.db.PendingManifests()
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(pending, func(a, b adapters.SessionRef) int { return strings.Compare(a.Agent, b.Agent) })
	if diff := cmp.Diff(sessions, pending); diff != "" {
		t.Fatalf("pending after recovering copied manifests (-want +got): %s", diff)
	}
	want := map[adapters.SessionRef][]string{}
	for _, ref := range sessions {
		if want[ref] = snapshots(t, sy, ref); len(want[ref]) != 1 {
			t.Fatalf("%v recovered post_hoc snapshots = %v, want the 0.3.0 one", ref, want[ref])
		}
	}

	// Imported but never applied, as after a crash mid-recovery: the next recovery applies them.
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"DELETE FROM archive_applied", "DELETE FROM sessions", "DELETE FROM pending_manifests", "UPDATE archive_checkpoint SET clean = 0"} {
		if _, err := db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	sy = open(t, stateDir)
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, ref := range sessions {
		if got := snapshots(t, sy, ref); !slices.Equal(got, want[ref]) {
			t.Fatalf("%v post_hoc snapshots after applying imported manifests = %v, want %v", ref, got, want[ref])
		}
	}

	if sum, err := sy.Run(t.Context(), nil); err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	for _, ref := range sessions {
		p, _, err := sy.current("session", ref.Agent, ref.Key)
		if err != nil || p == nil || !slices.ContainsFunc(p.Manifest.Snapshots, func(s state.SnapshotEntry) bool { return s.ID == want[ref][0] }) {
			t.Fatalf("%v republished manifest lacks snapshot %s: %v", ref, want[ref][0], err)
		}
	}

	if err := os.RemoveAll(filepath.Join(bucketDir, "v3", "machines", testenv.MachineID, "records", "manifest")); err != nil {
		t.Fatal(err)
	}
	// The upgraded state forgets the deleted manifests, warm or cold.
	if sum, err := open(t, stateDir).Run(t.Context(), nil); err != nil || sum.Errors != 0 {
		t.Fatalf("warm Run after deleting copied manifests = %+v, %v", sum, err)
	}
	cold := open(t, t.TempDir())
	if err := cold.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, ref := range sessions {
		if got := snapshots(t, cold, ref); !slices.Contains(got, want[ref][0]) {
			t.Fatalf("%v post_hoc snapshots after deleting copied manifests = %v, want %v", ref, got, want[ref])
		}
	}
	if pending, err := cold.db.PendingManifests(); len(pending) != 0 || err != nil {
		t.Fatalf("pending after deleting copied manifests = %v, %v", pending, err)
	}
}
