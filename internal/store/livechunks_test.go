package store_test

import (
	"bytes"
	"context"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/syncer"
	"github.com/mich-murphy/memory/internal/testenv"
)

// chunkAdapter exports one closed, synthetic binary fixture. It deliberately does
// not claim SQLite integrity: native CLI acceptance covers actual SQL/backup use.
// Agent roots are under the disposable home. No launches or cwd are supplied,
// so no managed settings or ancestor snapshots are collected.
type chunkAdapter struct {
	adapters.Adapter
	t      *testing.T
	source string
}

func (a *chunkAdapter) Classify(string, string) (adapters.SessionFile, bool) {
	return adapters.SessionFile{}, false
}

func (a *chunkAdapter) Exports(root, workDir string, _ time.Duration) adapters.Discovery {
	a.t.Helper()
	body, err := os.ReadFile(a.source)
	acceptanceOK(a.t, "read closed synthetic database fixture", err)
	path := filepath.Join(workDir, "synthetic", "database.bin")
	acceptanceOK(a.t, "create private database export directory", os.MkdirAll(filepath.Dir(path), 0o700))
	acceptanceOK(a.t, "write consistent private database export", os.WriteFile(path, body, 0o600))
	return adapters.Discovery{Complete: true, Files: []adapters.SessionFile{{
		Agent: a.Name(), Root: root, Path: path, SourcePath: a.source,
		Key: "db/synthetic.bin", Role: adapters.RoleDB,
	}}}
}

// Twenty-one unique misses exceed the sixteen-worker dependency pool. The last
// of twenty-two 1 MiB chunks repeats chunk five, exercising within-capture dedup.
func acceptanceChunkBytes(t *testing.T) []byte {
	t.Helper()
	body := make([]byte, 22<<20)
	rng := rand.NewChaCha8([32]byte{22, 8, 1, 16})
	_, err := rng.Read(body)
	acceptanceOK(t, "generate deterministic synthetic database bytes", err)
	copy(body, "SQLite format 3\x00") // page/header-like fixture, not a SQL database
	copy(body[21<<20:], body[5<<20:6<<20])
	return body
}

func acceptanceChunks(ctx context.Context, t *testing.T, bucket *namespace, destination *config.Config, backend string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	acceptanceOK(t, "resolve synthetic database home", err)
	cfg := *destination
	cfg.Home, cfg.StateDir, cfg.MachineID, cfg.Hostname = home, filepath.Join(home, "state"), "acceptance", "synthetic"
	cfg.Path = filepath.Join(home, "absent-config.env")
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_CONFIG", cfg.Path)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
	a := &chunkAdapter{Adapter: adapters.NewClaude(home), t: t, source: filepath.Join(home, "database.bin")}
	acceptanceOK(t, "create synthetic database root", os.MkdirAll(a.CanonicalRoot(), 0o700))
	initial := acceptanceChunkBytes(t)
	changed := bytes.Clone(initial)
	changed[24] ^= 1 // header-like update affects exactly the first fixed chunk
	versions := [][]byte{initial, changed}
	passes := []struct {
		phase string
		body  []byte
	}{{"cold", initial}, {"reuse", initial}, {"header", changed}}
	var history []testenv.Record
	var sf adapters.SessionFile
	for pass, capture := range passes {
		body := capture.body
		acceptanceOK(t, "write synthetic database source", os.WriteFile(a.source, body, 0o600))
		before := bucket.blobPuts()
		priorBlobs, err := bucket.keys(ctx, "v3/blobs/")
		acceptanceOK(t, "enumerate prior database blobs", err)
		started := time.Now()
		acceptanceChunkEngine(ctx, t, &cfg, bucket, a, func(sy *syncer.Syncer, db *state.State) {
			files, err := sy.SweepFiles(ctx, []string{a.Name()})
			acceptanceOK(t, "discover consistent database export", err)
			if len(files) != 1 || files[0].Role != adapters.RoleDB || files[0].SourcePath != a.source {
				t.Fatal("database discovery did not return exactly the synthetic export")
			}
			sf = files[0]
			sum, err := sy.Run(ctx, files)
			elapsed := time.Since(started).Seconds()
			acceptanceOK(t, "publish database capture", err)
			if sum.Errors != 0 {
				t.Fatal("database capture reported errors (details withheld)")
			}
			r := acceptanceCurrent(t, db, sf.Agent, sf.Key)
			decoded, err := testenv.DecodeRecord(r.Key, r.Body)
			acceptanceOK(t, "independently decode database descriptor", err)
			acceptanceChunkLayout(t, decoded, body)
			var newPieces []raw.Piece
			switch pass {
			case 0:
				if decoded.Sequence != 1 || decoded.Parent != "" {
					t.Fatal("cold database capture did not start a new chain")
				}
				for _, p := range decoded.File.Pieces {
					if slices.Contains(priorBlobs, p.Object) {
						t.Fatal("cold database chunk was already present")
					}
				}
				newPieces = decoded.File.Pieces
				history = append(history, decoded)
			case 1:
				if decoded.ID != history[0].ID || sum.Uploaded != 0 || sum.Unchanged != 1 {
					t.Fatal("unchanged database did not reuse its acknowledged generation")
				}
			case 2:
				previous := history[0]
				if decoded.Sequence != 2 || decoded.Parent != previous.ID || decoded.File.Pieces[0].Object == previous.File.Pieces[0].Object || !slices.Equal(decoded.File.Pieces[1:], previous.File.Pieces[1:]) {
					t.Fatal("database header update changed more than its first chunk or broke lineage")
				}
				newPieces = decoded.File.Pieces[:1]
				history = append(history, decoded)
			}
			newBytes := acceptanceChunkPuts(t, bucket, before, newPieces)
			if sum.BytesUploaded != newBytes || pass != 1 && sum.Uploaded != 1 {
				t.Fatal("database publication upload accounting differs")
			}
			t.Logf("db_chunks backend=%s phase=%s publish_seconds=%.3f source_bytes=%d chunks=%d new_blob_bytes=%d", backend, capture.phase, elapsed, len(body), len(decoded.File.Pieces), newBytes)
		})
		acceptanceAudit(ctx, t, bucket)
	}
	// Remove source, consistent export, staging and the entire SQLite cache.
	// Repeat recovery with another complete cache loss, never rediscovering sources.
	acceptanceOK(t, "remove synthetic database source", os.Remove(a.source))
	for range 2 {
		acceptanceOK(t, "lose entire database capture cache", os.RemoveAll(cfg.StateDir))
		before := bucket.blobPuts()
		acceptanceChunkEngine(ctx, t, &cfg, bucket, a, func(sy *syncer.Syncer, db *state.State) {
			acceptanceOK(t, "cold database Recover", sy.Recover(ctx))
			if acceptanceCurrent(t, db, sf.Agent, sf.Key).ID != history[1].ID {
				t.Fatal("cold database recovery selected the wrong current")
			}
			for i := range history {
				r := &history[i]
				var restored bytes.Buffer
				acceptanceOK(t, "restore database history after source/cache loss", sy.Restore(ctx, sf.Agent, sf.Key, r.ID, &restored))
				if !bytes.Equal(restored.Bytes(), versions[i]) {
					t.Fatal("restored database history bytes differ")
				}
				physical, err := testenv.Content(r.File, func(key string) ([]byte, error) { return bucket.Get(ctx, key) })
				acceptanceOK(t, "independently reconstruct database history", err)
				if !bytes.Equal(physical, restored.Bytes()) {
					t.Fatal("physical database reconstruction differs from restored bytes")
				}
			}
			var current bytes.Buffer
			acceptanceOK(t, "restore current database after cache loss", sy.Restore(ctx, sf.Agent, sf.Key, "", &current))
			if !bytes.Equal(current.Bytes(), changed) {
				t.Fatal("current database restored bytes differ")
			}
		})
		acceptanceChunkPuts(t, bucket, before, nil)
		acceptanceAudit(ctx, t, bucket)
	}
	t.Logf("db_chunks backend=%s restored_generations=2 cold_recoveries=2 source_absent=true cache_lost=true byte_exact=true", backend)
}

func acceptanceChunkEngine(ctx context.Context, t *testing.T, cfg *config.Config, bucket *namespace, a *chunkAdapter, use func(*syncer.Syncer, *state.State)) {
	t.Helper()
	db, err := state.Open(ctx, cfg.StateDir)
	acceptanceOK(t, "open disposable database capture cache", err)
	defer func() { acceptanceOK(t, "close disposable database capture cache", db.Close()) }()
	sy := syncer.New(cfg, bucket, db, map[string]adapters.Adapter{a.Name(): a}, syncer.Options{Logger: slog.New(slog.DiscardHandler), Trigger: "acceptance", Version: "test"})
	use(sy, db)
}

func acceptanceChunkLayout(t *testing.T, r testenv.Record, body []byte) {
	t.Helper()
	g := r.File
	if g == nil || g.Role != string(adapters.RoleDB) || g.Kind != "capture" || g.Size != int64(len(body)) || g.Size < 8<<20 || g.SHA256 != raw.SHA256(body) || len(g.Pieces) != 22 {
		t.Fatal("database did not publish the expected threshold-sized fixed-chunk capture")
	}
	unique := map[string]bool{}
	for i, p := range g.Pieces {
		if p.Start != int64(i)<<20 || p.End != int64(i+1)<<20 || p.SHA256 != raw.SHA256(body[p.Start:p.End]) {
			t.Fatal("database fixed chunk offsets or content identity differ")
		}
		unique[p.Object] = true
	}
	if len(unique) != 21 || g.Pieces[21].Object != g.Pieces[5].Object {
		t.Fatal("database fixture did not exercise more than sixteen unique chunks and one repeated chunk")
	}
}

// Count attempted writes, not just the final object set: overwriting/re-uploading
// an unchanged content-addressed object must fail this oracle as well.
func acceptanceChunkPuts(t *testing.T, bucket *namespace, before map[string]int, pieces []raw.Piece) int64 {
	t.Helper()
	got := map[string]int{}
	for key, count := range bucket.blobPuts() {
		if delta := count - before[key]; delta != 0 {
			got[key] = delta
		}
	}
	want := map[string]int{}
	var size int64
	for _, p := range pieces {
		if want[p.Object] == 0 {
			want[p.Object] = 1
			size += p.EncodedSize
		}
	}
	if !cmp.Equal(got, want) {
		t.Fatal("database blob PUTs differ from exactly the unique new chunks (identities withheld)")
	}
	return size
}
