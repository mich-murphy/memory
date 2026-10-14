package syncer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

func TestImmutableFileHistoryRecoversAfterCacheAndSourceLoss(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	stateDir := t.TempDir()
	bucket := newMemStore()
	cfg := &config.Config{Home: root, StateDir: stateDir, LocalStore: t.TempDir(), MachineID: "machine"}
	sf := adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: "file", Role: adapters.RoleAux}
	for _, body := range []string{"A", "AB", "C", ""} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := state.Open(t.Context(), stateDir)
		if err != nil {
			t.Fatal(err)
		}
		sy := New(cfg, bucket, db, adapters.All(root), Options{})
		summary, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
		if err != nil {
			t.Fatal(err)
		}
		if summary.Errors != 0 {
			t.Fatalf("Run errors = %v", summary.ErrorMessages)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(t.Context(), stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sy := New(cfg, bucket, db, adapters.All(root), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err := db.ArchiveRecords("file", streamID(sf.Agent, sf.Key))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range records {
		if !r.Committed {
			continue
		}
		p, err := decodePublication(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var content []byte
		for _, piece := range p.File.Pieces {
			body, err := bucket.Get(context.Background(), piece.Object)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := raw.Decompress(body)
			if err != nil {
				t.Fatal(err)
			}
			content = append(content, decoded...)
		}
		if !bytes.Equal([]byte(raw.SHA256(content)), []byte(p.File.SHA256)) {
			t.Fatal("generation checksum differs")
		}
		got = append(got, string(content))
	}
	if len(got) != 4 || got[0] != "A" || got[1] != "AB" || got[2] != "C" || got[3] != "" {
		t.Fatalf("history = %#v", got)
	}
	sy.recovered = false
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, _, err := sy.current("file", sf.Agent, sf.Key)
	if err != nil {
		t.Fatal(err)
	}
	if current.File.Size != 0 {
		t.Fatal("recovery did not select empty current")
	}
}

func TestObsoleteIncompletePlanNeverCreatesAForkWhenItsBlobAppears(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	bucket := newMemStore()
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	sf := adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: "file", Role: adapters.RoleAux}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		sy := New(cfg, bucket, db, adapters.All(root), Options{})
		summary, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
		if err != nil {
			t.Fatal(err)
		}
		if summary.Errors != 0 {
			t.Fatalf("Run errors = %v", summary.ErrorMessages)
		}
	}
	write("A")
	sy := New(cfg, bucket, db, adapters.All(root), Options{})
	first, parent, err := sy.current("file", sf.Agent, sf.Key)
	if err != nil {
		t.Fatal(err)
	}
	encoded := raw.Compress([]byte("AB"))
	encodedSHA := raw.SHA256(encoded)
	obsolete := *first
	generation := *first.File
	generation.Sequence = 2
	generation.Parent = parent.ID
	generation.Size = 2
	generation.SHA256 = raw.SHA256([]byte("AB"))
	generation.Pieces = []raw.Piece{{Start: 0, End: 2, SHA256: generation.SHA256, EncodedSHA256: encodedSHA, EncodedSize: int64(len(encoded)), Object: raw.EncodedKey(encodedSHA)}}
	obsolete.File = &generation
	obsolete.Sequence = 2
	obsolete.Parent = parent.ID
	obsolete.Dependencies = []dependency{pieceDependency(generation.Pieces[0])}
	body, id, err := canonicalPublication(&obsolete)
	if err != nil {
		t.Fatal(err)
	}
	if err := bucket.Put(t.Context(), sy.publicationKey(&obsolete, false, id), body, map[string]string{"sha256": id}); err != nil {
		t.Fatal(err)
	}
	write("C") // pending AB is missing its dependency, so C legitimately becomes sequence 2
	if err := bucket.Put(t.Context(), raw.EncodedKey(encodedSHA), encoded, map[string]string{"sha256": generation.SHA256, "encoded_sha256": encodedSHA}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCheckpoint("forceCold", false); err != nil {
		t.Fatal(err)
	}
	recovered := New(cfg, bucket, db, adapters.All(root), Options{})
	if err := recovered.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := bucket.Head(t.Context(), recovered.publicationKey(&obsolete, true, id)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("obsolete commit = %v, want absent", err)
	}
	current, _, err := recovered.current("file", sf.Agent, sf.Key)
	if err != nil {
		t.Fatal(err)
	}
	if current.File.SHA256 != raw.SHA256([]byte("C")) {
		t.Fatal("obsolete plan changed current")
	}
}

type countedArchive struct {
	store.Store
	transcriptReads int
}

func (c *countedArchive) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.HasPrefix(key, "v3/blobs/zstd/") {
		c.transcriptReads++
	}
	return c.Store.OpenReader(ctx, key)
}

func TestWarmHookDoesNotDownloadArchivedTranscriptBodies(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	bucket := &countedArchive{Store: newMemStore()}
	sf := adapters.SessionFile{Agent: "claude", Root: root, Path: path, Key: "file", Role: adapters.RoleAux}
	if err := os.WriteFile(path, bytes.Repeat([]byte("line\n"), 10000), 0o600); err != nil {
		t.Fatal(err)
	}
	for iteration := range 2 {
		db, err := state.Open(t.Context(), cfg.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		sy := New(cfg, bucket, db, adapters.All(root), Options{})
		summary, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
		if err != nil {
			t.Fatal(err)
		}
		if summary.Errors != 0 {
			t.Fatalf("Run errors = %v", summary.ErrorMessages)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			bucket.transcriptReads = 0
		}
	}
	if bucket.transcriptReads != 0 {
		t.Fatalf("warm transcript OpenReader calls = %d, want 0", bucket.transcriptReads)
	}
}

func TestStreamedRestoreValidatesWholeContentAndRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	cfg := &config.Config{Home: root, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	bucket := newMemStore()
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	expected := bytes.Repeat([]byte("line\n"), 10000)
	if err := os.WriteFile(path, expected, 0o600); err != nil {
		t.Fatal(err)
	}
	sy := New(cfg, bucket, db, adapters.All(root), Options{})
	summary, err := sy.Run(t.Context(), []adapters.SessionFile{{Agent: "claude", Root: root, Path: path, Key: "file", Role: adapters.RoleAux}})
	if err != nil || summary.Errors != 0 {
		t.Fatalf("Run = %+v, %v", summary, err)
	}
	// Restore has no source dependency and also works with the same initialized reader.
	var got bytes.Buffer
	if err := sy.Restore(t.Context(), "claude", "file", "", &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), expected) {
		t.Fatal("Restore differs from capture")
	}
	current, _, err := sy.current("file", "claude", "file")
	if err != nil {
		t.Fatal(err)
	}
	corrupted := *current.File
	corrupted.SHA256 = raw.SHA256([]byte("wrong"))
	if err := sy.restoreGeneration(t.Context(), &corrupted, io.Discard); err == nil {
		t.Fatal("Restore accepted wrong whole digest")
	}
	object := current.File.Pieces[0].Object
	bucket.mu.Lock()
	stored := bucket.objects[object]
	stored.body[len(stored.body)/2] ^= 1
	bucket.objects[object] = stored
	bucket.mu.Unlock()
	if err := sy.Restore(t.Context(), "claude", "file", "", io.Discard); err == nil {
		t.Fatal("Restore accepted corrupted blob")
	}
}

func TestLaunchPublicationDependenciesAreCanonicalSets(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	path := filepath.Join(root, "projects", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		path: "{}\n", filepath.Join(root, "CLAUDE.md"): "User instructions", filepath.Join(home, "CLAUDE.md"): "Project instructions",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Home: home, StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	bucket := newMemStore()
	sy := New(cfg, bucket, db, reviewAdapters(home), Options{})
	for i := range 12 {
		e := launch.Envelope{Schema: 1, ID: fmt.Sprintf("canonical-%d", i), Agent: "claude", Path: path, RecordedAt: float64(i + 1), Payload: map[string]any{"cwd": home}}
		if err := sy.RecordEnvelope(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		p, r, err := sy.current("launch", e.Agent, e.ID)
		if err != nil || p == nil {
			t.Fatalf("current launch = %+v, %v", p, err)
		}
		if len(p.Dependencies) < 3 || !slices.IsSortedFunc(p.Dependencies, func(a, b dependency) int { return strings.Compare(a.Object, b.Object) }) {
			t.Fatalf("physical launch dependencies are not canonical: %+v", p.Dependencies)
		}
		if len(slices.Compact(slices.Clone(p.Dependencies))) != len(p.Dependencies) {
			t.Fatal("physical launch dependencies contain duplicates")
		}
		plan, err := bucket.Get(t.Context(), strings.Replace(r.Key, "/commits/", "/plans/", 1))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(plan, r.Body) {
			t.Fatal("plan and commit canonical bytes differ")
		}
	}
	// Force a noncanonical input, rather than relying on the runtime's random map order.
	p, _, err := sy.current("launch", "claude", "canonical-11")
	if err != nil {
		t.Fatal(err)
	}
	p.Key = "reordered"
	p.Launch.Envelope.ID = p.Key
	count := len(p.Dependencies)
	slices.Reverse(p.Dependencies)
	p.Dependencies = append(p.Dependencies, p.Dependencies[0])
	r, err := sy.publish(t.Context(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	published, err := decodePublication(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(published.Dependencies) != count || !slices.IsSortedFunc(published.Dependencies, func(a, b dependency) int { return strings.Compare(a.Object, b.Object) }) {
		t.Fatalf("published dependencies = %+v, want canonical deduplicated set", published.Dependencies)
	}
}
