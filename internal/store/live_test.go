package store_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/syncer"
	"github.com/mich-murphy/memory/internal/testenv"
)

// TestR2LiveAcceptance is intentionally separate from the legacy TestR2Live.
// Run ONLY the exact test name after review and explicit operator authorisation.
func TestR2LiveAcceptance(t *testing.T) {
	if os.Getenv("AGENT_SESSIONS_TEST_R2") != "1" {
		t.Skip("live R2 acceptance requires explicit AGENT_SESSIONS_TEST_R2=1")
	}
	cfg := &config.Config{
		R2Endpoint: os.Getenv("R2_ENDPOINT"), R2Bucket: os.Getenv("R2_BUCKET"),
		R2AccessKeyID: os.Getenv("R2_ACCESS_KEY_ID"), R2SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
	}
	r2, err := store.NewR2(cfg)
	acceptanceOK(t, "NewR2", err)
	runR2Acceptance(t, r2, cfg, true)
}

// The same archive and protocol oracles run normally without live access.
// gofakes3 returns NoSuchUpload before its first multipart initiation; scoped
// multipart inspection is covered separately by TestR2TestMultipart.
func TestR2AcceptanceFake(t *testing.T) {
	backend := s3mem.New()
	acceptanceOK(t, "CreateBucket", backend.CreateBucket("acceptance"))
	server := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(server.Close)
	cfg := &config.Config{R2Endpoint: server.URL, R2Bucket: "acceptance", R2AccessKeyID: "fake", R2SecretAccessKey: "fake"}
	r2, err := store.NewR2(cfg)
	acceptanceOK(t, "NewR2", err)
	runR2Acceptance(t, r2, cfg, false)
}

// Never format raw SDK errors, configs, summaries, tokens or downloaded bodies.
// The adapter also withholds SDK error text from the engine's own error messages.
func acceptanceOK(t *testing.T, action string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s failed (details withheld)", action)
	}
}

func runR2Acceptance(t *testing.T, r2 *store.R2, cfg *config.Config, inspectMultipart bool) {
	t.Helper()
	store.SetMultipart(t, 1<<20, 5<<20) // 11 MiB must exercise three actual upload parts.
	bucket, err := newNamespace(r2, 2)
	acceptanceOK(t, "newNamespace", err)
	// Register before even the read-only preflight. A collision is NOT owned;
	// only a fully empty preflight authorises mutation and subsequent cleanup.
	owned := false
	t.Cleanup(func() {
		if !owned {
			t.Log("cleanup skipped: no mutation and prefix ownership not established")
			return
		}
		if inspectMultipart {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 2*time.Minute)
			aborted, err := store.R2TestMultipart(ctx, r2, bucket.prefix, true)
			cancel()
			if err != nil {
				t.Error("owned multipart cleanup/empty verification failed (details withheld)")
			} else {
				t.Logf("cleanup prefix=%s aborted_uploads=%d remaining_uploads=0", bucket.prefix, aborted)
			}
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 2*time.Minute)
		defer cancel()
		deleted, err := bucket.clean(ctx)
		if err != nil {
			t.Error("owned object cleanup/empty verification failed (details withheld)")
		} else {
			t.Logf("cleanup prefix=%s deleted_objects=%d remaining_objects=0", bucket.prefix, deleted)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	keys, err := bucket.keys(ctx, "")
	acceptanceOK(t, "owned prefix preflight", err)
	if len(keys) != 0 {
		t.Fatal("fresh random prefix was not empty; refusing mutation")
	}
	if inspectMultipart {
		count, err := store.R2TestMultipart(ctx, r2, bucket.prefix, false)
		if err != nil {
			t.Fatalf("owned multipart preflight: %s", err.Error()) // test-only helper returns fixed, credential-free errors
		}
		if count != 0 {
			t.Fatal("fresh random prefix had multipart uploads; refusing mutation")
		}
	}
	owned = true
	t.Logf("acceptance prefix=%s preflight_objects=0", bucket.prefix)
	acceptanceProtocol(ctx, t, bucket)
	acceptanceArchive(ctx, t, bucket, cfg)
	if inspectMultipart {
		count, err := store.R2TestMultipart(ctx, r2, bucket.prefix, false)
		acceptanceOK(t, "multipart completion verification", err)
		if count != 0 {
			t.Fatalf("owned multipart uploads = %d, want 0", count)
		}
	}
}

func acceptanceProtocol(ctx context.Context, t *testing.T, bucket *namespace) {
	t.Helper()
	original := []byte("synthetic original\x00bytes\n")
	acceptanceOK(t, "Create absent", bucket.Create(ctx, "protocol/create", original))
	if err := bucket.Create(ctx, "protocol/create", []byte("different")); !errors.Is(err, store.ErrExists) {
		t.Fatal("Create existing did not return ErrExists")
	}
	body, err := bucket.Get(ctx, "protocol/create")
	acceptanceOK(t, "Get existing Create", err)
	if !bytes.Equal(body, original) {
		t.Fatal("Create existing replaced original bytes")
	}
	meta := map[string]string{"sha256": raw.SHA256(original), "role": "synthetic"}
	acceptanceOK(t, "Put metadata", bucket.Put(ctx, "protocol/body", original, meta))
	obj, err := bucket.Head(ctx, "protocol/body")
	acceptanceOK(t, "Head metadata", err)
	if obj.Key != "protocol/body" || obj.Size != int64(len(original)) || !cmp.Equal(obj.Metadata, meta) {
		t.Fatal("Head metadata, size or logical key differs")
	}
	for _, key := range []string{"protocol/body", "protocol/create"} {
		acceptanceRead(ctx, t, bucket, key, original)
	}
	ok, err := bucket.Copy(ctx, "protocol/body", "protocol/copy", meta["sha256"])
	acceptanceOK(t, "Copy", err)
	if !ok {
		t.Fatal("Copy did not confirm destination")
	}
	acceptanceRead(ctx, t, bucket, "protocol/copy", original)
	if _, err := bucket.Copy(ctx, "../outside", "protocol/guard", meta["sha256"]); err == nil {
		t.Fatal("Copy accepted parent escape")
	}
	if err := bucket.Delete(ctx, "_agent-sessions/test/other/object"); err == nil {
		t.Fatal("Delete accepted another prefix")
	}
	if _, err := bucket.List(ctx, "../", "", 1); err == nil {
		t.Fatal("List accepted parent escape")
	}
	keys, err := bucket.keys(ctx, "protocol/")
	acceptanceOK(t, "real paginated List", err)
	if diff := cmp.Diff([]string{"protocol/body", "protocol/copy", "protocol/create"}, keys); diff != "" {
		t.Fatal("paginated List differs from exact expected logical keys")
	}
	if bucket.continuationPages() == 0 {
		t.Fatal("real small-limit LIST did not return a continuation token")
	}
	big := make([]byte, 11<<20)
	_, err = rand.Read(big)
	acceptanceOK(t, "synthetic multipart bytes", err)
	path := filepath.Join(t.TempDir(), "multipart")
	acceptanceOK(t, "write synthetic multipart source", os.WriteFile(path, big, 0o600))
	acceptanceOK(t, "multipart PutFile", bucket.PutFile(ctx, "protocol/multipart", path, map[string]string{"sha256": raw.SHA256(big)}))
	acceptanceRead(ctx, t, bucket, "protocol/multipart", big)
	obj, err = bucket.Head(ctx, "protocol/multipart")
	acceptanceOK(t, "multipart Head", err)
	if obj.Size != int64(len(big)) || obj.Metadata["sha256"] != raw.SHA256(big) {
		t.Fatal("multipart metadata or size differs")
	}
	t.Logf("protocol create_preserved=true metadata=true pagination=true crossprefix_rejected=true multipart_bytes=%d multipart_sha256=%s", len(big), raw.SHA256(big))
}

func acceptanceRead(ctx context.Context, t *testing.T, bucket store.Store, key string, want []byte) {
	t.Helper()
	body, err := bucket.Get(ctx, key)
	acceptanceOK(t, "Get exact data", err)
	if !bytes.Equal(body, want) {
		t.Fatal("Get data differs")
	}
	reader, err := bucket.OpenReader(ctx, key)
	acceptanceOK(t, "OpenReader", err)
	body, err = io.ReadAll(reader)
	closeErr := reader.Close()
	acceptanceOK(t, "OpenReader read", err)
	acceptanceOK(t, "OpenReader close", closeErr)
	if !bytes.Equal(body, want) {
		t.Fatal("OpenReader data differs")
	}
}

// No launches/cwd/snapshots: this fixture cannot traverse ancestor instructions,
// managed agent configuration, real HOME or any actual transcript source.
func acceptanceArchive(ctx context.Context, t *testing.T, bucket *namespace, destination *config.Config) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	acceptanceOK(t, "resolve fake home", err)
	cfg := *destination
	cfg.Home, cfg.StateDir, cfg.MachineID, cfg.Hostname = home, filepath.Join(home, "state"), "acceptance", "synthetic"
	cfg.Path = filepath.Join(home, "absent-config.env")
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "XDG_CONFIG_HOME", "AGENT_SESSIONS_DISABLE"} {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_CONFIG", cfg.Path)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
	root := filepath.Join(home, ".claude")
	acceptanceOK(t, "create private synthetic root", os.MkdirAll(root, 0o700))
	path := filepath.Join(root, "notes.txt")
	sf := adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: "notes.txt", SessionKey: "synthetic", Role: adapters.RoleAux}
	want := []string{"A", "AB", "C", ""}
	var ids []string
	for i, body := range want {
		acceptanceOK(t, "write synthetic generation", os.WriteFile(path, []byte(body), 0o600))
		acceptanceEngine(ctx, t, &cfg, bucket, func(sy *syncer.Syncer, db *state.State) {
			acceptanceRun(ctx, t, sy, []adapters.SessionFile{sf})
			record := acceptanceCurrent(t, db, sf.Agent, sf.Key)
			ids = append(ids, record.ID)
			if record.Sequence != uint64(i+1) {
				t.Fatal("capture sequence differs")
			}
		})
		acceptanceAudit(ctx, t, bucket)
	}
	acceptanceOK(t, "remove entire SQLite directory", os.RemoveAll(cfg.StateDir))
	acceptanceOK(t, "remove synthetic source", os.Remove(path))
	// Cold recovery selects the empty capture before complete discovery deletes it.
	acceptanceEngine(ctx, t, &cfg, bucket, func(sy *syncer.Syncer, db *state.State) {
		acceptanceOK(t, "cold Recover", sy.Recover(ctx))
		acceptanceRestoreHistory(ctx, t, sy, sf, ids, want)
		if record := acceptanceCurrent(t, db, sf.Agent, sf.Key); record.ID != ids[3] {
			t.Fatal("cold recovery did not select empty current")
		}
		files, err := sy.SweepFiles(ctx, []string{"claude"})
		acceptanceOK(t, "complete synthetic discovery", err)
		acceptanceRun(ctx, t, sy, files)
		record := acceptanceCurrent(t, db, sf.Agent, sf.Key)
		decoded, err := testenv.DecodeRecord(record.Key, record.Body)
		acceptanceOK(t, "decode current tombstone", err)
		if record.Sequence != 5 || decoded.File.Kind != "delete" || decoded.File.DeletedAt <= 0 || decoded.File.SHA256 != raw.SHA256(nil) {
			t.Fatal("current is not the expected empty-content tombstone")
		}
		ids = append(ids, record.ID)
		want = append(want, "")
	})
	// Losing *all* state twice forces two independent remote metadata reconciliations.
	for range 2 {
		acceptanceOK(t, "repeat entire SQLite loss", os.RemoveAll(cfg.StateDir))
		acceptanceEngine(ctx, t, &cfg, bucket, func(sy *syncer.Syncer, db *state.State) {
			acceptanceOK(t, "repeat cold Recover", sy.Recover(ctx))
			acceptanceRestoreHistory(ctx, t, sy, sf, ids, want)
			if record := acceptanceCurrent(t, db, sf.Agent, sf.Key); record.ID != ids[4] {
				t.Fatal("repeated cold recovery changed tombstone current")
			}
		})
		acceptanceAudit(ctx, t, bucket)
	}
	acceptanceRemoteExclusion(ctx, t, &cfg, bucket)
	keys, err := bucket.keys(ctx, "v3/")
	acceptanceOK(t, "final archive enumeration", err)
	t.Logf("archive acknowledged_generations=4 restored_generations=5 cold_recoveries=3 tombstone_current=true retained_optout=true objects=%d continuation_replies=%d", len(keys), bucket.continuationPages())
	for i, body := range want {
		t.Logf("generation sequence=%d bytes=%d sha256=%s descriptor_sha256=%s", i+1, len(body), raw.SHA256([]byte(body)), ids[i])
	}
}

func acceptanceEngine(ctx context.Context, t *testing.T, cfg *config.Config, bucket store.Store, use func(*syncer.Syncer, *state.State)) {
	t.Helper()
	db, err := state.Open(ctx, cfg.StateDir)
	acceptanceOK(t, "state.Open", err)
	defer func() { acceptanceOK(t, "state.Close", db.Close()) }()
	sy := syncer.New(cfg, bucket, db, adapters.All(cfg.Home), syncer.Options{Logger: slog.New(slog.DiscardHandler), Trigger: "acceptance", Version: "test"})
	use(sy, db)
}

func acceptanceRun(ctx context.Context, t *testing.T, sy *syncer.Syncer, files []adapters.SessionFile) {
	t.Helper()
	sum, err := sy.Run(ctx, files)
	acceptanceOK(t, "Syncer.Run", err)
	if sum.Errors != 0 {
		t.Fatalf("Syncer.Run errors = %d, want 0 (details withheld)", sum.Errors)
	}
}

func acceptanceCurrent(t *testing.T, db *state.State, agent, key string) *state.Record {
	t.Helper()
	record, err := db.CurrentArchive("file", raw.SHA256([]byte(agent+"\x00"+key)))
	acceptanceOK(t, "CurrentArchive", err)
	if record == nil {
		t.Fatal("CurrentArchive missing acknowledged generation")
	}
	return record
}

func acceptanceRestoreHistory(ctx context.Context, t *testing.T, sy *syncer.Syncer, sf adapters.SessionFile, ids, want []string) {
	t.Helper()
	for i, id := range ids {
		var restored bytes.Buffer
		acceptanceOK(t, "Restore acknowledged generation", sy.Restore(ctx, sf.Agent, sf.Key, id, &restored))
		if restored.String() != want[i] {
			t.Fatalf("Restore generation %d data differs", i+1)
		}
	}
	var current bytes.Buffer
	acceptanceOK(t, "Restore current", sy.Restore(ctx, sf.Agent, sf.Key, "", &current))
	if current.String() != want[len(want)-1] {
		t.Fatal("Restore current differs")
	}
}

func acceptanceAudit(ctx context.Context, t *testing.T, bucket *namespace) {
	t.Helper()
	keys, err := bucket.keys(ctx, "v3/")
	acceptanceOK(t, "audit LIST", err)
	acceptanceOK(t, "independent physical v3 audit", testenv.Audit(keys, func(key string) ([]byte, error) { return bucket.Get(ctx, key) }))
}

func acceptanceRemoteExclusion(ctx context.Context, t *testing.T, cfg *config.Config, bucket *namespace) {
	t.Helper()
	root := filepath.Join(cfg.Home, ".claude")
	path := filepath.Join(root, "projects", "synthetic", "private.jsonl")
	acceptanceOK(t, "create private synthetic session", os.MkdirAll(filepath.Dir(path), 0o700))
	acceptanceOK(t, "write pre-exclusion synthetic data", os.WriteFile(path, []byte("prior\n"), 0o600))
	a := adapters.NewClaude(cfg.Home)
	sf, ok := a.Classify(path, root)
	if !ok {
		t.Fatal("synthetic exclusion source did not classify")
	}
	var priorID string
	acceptanceEngine(ctx, t, cfg, bucket, func(sy *syncer.Syncer, db *state.State) {
		acceptanceRun(ctx, t, sy, []adapters.SessionFile{sf})
		priorID = acceptanceCurrent(t, db, sf.Agent, sf.Key).ID
	})
	_, err := exclusion.RecordPath(cfg.StateDir, a, path)
	acceptanceOK(t, "persist synthetic optout", err)
	const sentinel = "SYNTHETIC_OPTED_OUT_BYTES_MUST_NEVER_BE_CAPTURED"
	acceptanceOK(t, "write excluded synthetic data", os.WriteFile(path, []byte(sentinel+"\n"), 0o600))
	// A distinct session in the same root is not a shared root-wide file. It
	// must remain discoverable and capturable, ruling out blanket suppression.
	allowedPath := filepath.Join(filepath.Dir(path), "allowed.jsonl")
	allowed, ok := a.Classify(allowedPath, root)
	if !ok || allowed.SessionKey == "" || allowed.SessionKey == sf.SessionKey {
		t.Fatal("allowed synthetic control did not classify as a distinct session")
	}
	allowedBodies := []string{
		"{\"sessionId\":\"allowed\",\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"synthetic allowed before cache loss\"}}\n",
		"{\"sessionId\":\"allowed\",\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"synthetic allowed after cache loss\"}}\n",
	}
	var allowedIDs []string
	for pass, body := range allowedBodies {
		// Change the control after cache loss: the second pass must acknowledge
		// new bytes, not merely restore an already recovered generation.
		acceptanceOK(t, "write allowed synthetic control", os.WriteFile(allowedPath, []byte(body), 0o600))
		acceptanceEngine(ctx, t, cfg, bucket, func(sy *syncer.Syncer, db *state.State) {
			files, err := sy.SweepFiles(ctx, []string{"claude"})
			acceptanceOK(t, "recover optout before discovery", err)
			if slices.ContainsFunc(files, func(found adapters.SessionFile) bool { return found.Path == path }) {
				t.Fatal("remote optout did not suppress source before capture")
			}
			if !slices.ContainsFunc(files, func(found adapters.SessionFile) bool {
				return found.Path == allowedPath && found.Key == allowed.Key && found.SessionKey == allowed.SessionKey
			}) {
				t.Fatal("optout suppressed the unexcluded synthetic session control")
			}
			acceptanceRun(ctx, t, sy, files)
			control := acceptanceCurrent(t, db, allowed.Agent, allowed.Key)
			if control.Sequence != uint64(pass+1) || slices.Contains(allowedIDs, control.ID) {
				t.Fatal("allowed synthetic control did not acknowledge a new generation")
			}
			allowedIDs = append(allowedIDs, control.ID)
			acceptanceRestoreHistory(ctx, t, sy, allowed, allowedIDs, allowedBodies[:pass+1])
			if acceptanceCurrent(t, db, sf.Agent, sf.Key).ID != priorID {
				t.Fatal("optout modified retained archived history")
			}
			acceptanceRestoreHistory(ctx, t, sy, sf, []string{priorID}, []string{"prior\n"})
		})
		t.Logf("optout allowed_control_capture_pass=%d bytes=%d sha256=%s excluded_absent=true prior_history_retained=true", pass+1, len(body), raw.SHA256([]byte(body)))
		if pass == 0 {
			acceptanceOK(t, "lose SQLite and local exclusions", os.RemoveAll(cfg.StateDir))
		}
	}
	keys, err := bucket.keys(ctx, "v3/")
	acceptanceOK(t, "optout physical enumeration", err)
	for _, key := range keys {
		body, err := bucket.Get(ctx, key)
		acceptanceOK(t, "optout physical read", err)
		if strings.HasSuffix(key, ".zst") {
			body, err = raw.Decompress(body)
			acceptanceOK(t, "optout physical decode", err)
		}
		if bytes.Contains(body, []byte(sentinel)) {
			t.Fatal("excluded synthetic sentinel reached archive")
		}
	}
	acceptanceAudit(ctx, t, bucket)
}
