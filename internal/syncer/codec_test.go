package syncer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/snapshot"
	"github.com/mich-murphy/memory/internal/state"
)

func TestNumericLaunchAndManifestProvenanceSurvivesColdRecovery(t *testing.T) {
	cfg := &config.Config{Home: t.TempDir(), StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db != nil {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	bucket := newMemStore()
	sy := New(cfg, bucket, db, reviewAdapters(cfg.Home), Options{})
	e := contractLaunch(t, sy)
	e.Payload["exact_integer"] = json.Number("9007199254740993")
	e.Payload["scaled_decimal"] = json.Number("1.00")
	e.Payload["nested"] = []any{json.Number("1e+20"), map[string]any{"small": json.Number("0.00000000000000000001")}}
	if err := sy.RecordEnvelope(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	p, launchCommit, err := sy.current("launch", e.Agent, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	ref := adapters.SessionRef{Agent: e.Agent, Key: p.Launch.SessionKey}
	sess, err := sy.db.Session(ref)
	if err != nil {
		t.Fatal(err)
	}
	sess.Info["exact_integer"] = e.Payload["exact_integer"]
	sess.Info["scaled_decimal"] = e.Payload["scaled_decimal"]
	if err := sy.db.CacheSession(ref, sess); err != nil {
		t.Fatal(err)
	}
	sum, err := sy.Run(t.Context(), nil)
	if err != nil || sum.Errors != 0 || sum.Manifests != 1 {
		t.Fatalf("Run = %+v, %v, want valid committed manifest", sum, err)
	}
	_, manifestCommit, err := sy.current("manifest", ref.Agent, ref.Key)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []*state.Record{launchCommit, manifestCommit} {
		decoded, err := decodePublication(record.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := decoded.validate(cfg.MachineID); err != nil {
			t.Fatalf("valid emitted descriptor rejected: %v", err)
		}
		body, id, err := canonicalPublication(decoded)
		if err != nil || !bytes.Equal(body, record.Body) || id != record.ID {
			t.Fatalf("numeric descriptor identity changed on decoding: %v", err)
		}
	}
	// Delete both the old cache and source, then recover solely from immutable records.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = nil
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.Path); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	sy = New(cfg, bucket, db, reviewAdapters(cfg.Home), Options{})
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	launchView, recoveredLaunch, err := sy.current("launch", e.Agent, e.ID)
	if err != nil || !bytes.Equal(recoveredLaunch.Body, launchCommit.Body) {
		t.Fatalf("cold launch identity = %+v, %v", recoveredLaunch, err)
	}
	manifestView, recoveredManifest, err := sy.current("manifest", ref.Agent, ref.Key)
	if err != nil || !bytes.Equal(recoveredManifest.Body, manifestCommit.Body) {
		t.Fatalf("cold manifest identity = %+v, %v", recoveredManifest, err)
	}
	for _, values := range []map[string]any{launchView.Launch.Envelope.Payload, manifestView.Manifest.Info} {
		for key, want := range map[string]string{"exact_integer": "9007199254740993", "scaled_decimal": "1.00"} {
			got, ok := values[key].(json.Number)
			if !ok || got.String() != want {
				t.Fatalf("cold %s = %v (%T), want exact JSON number %s", key, values[key], values[key], want)
			}
		}
	}
	projected, err := db.Session(ref)
	if err != nil || projected.Info["exact_integer"] != json.Number("9007199254740993") || projected.Info["scaled_decimal"] != json.Number("1.00") {
		t.Fatalf("cold session projection = %+v, %v", projected, err)
	}
}

func TestNonUTF8AuthoritativeIdentitiesAreRejectedBeforePlanUpload(t *testing.T) {
	bad := string([]byte{0xff})
	for _, tc := range []struct {
		name, kind string
		mutate     func(*publication)
	}{
		{"machine", "root", func(p *publication) { p.MachineID += bad }},
		{"key", "root", func(p *publication) { p.Key += bad }},
		{"rootPath", "root", func(p *publication) { p.Root.Path += bad; p.Key = streamID(p.Agent, p.Root.Path) }},
		{"fileKey", "file", func(p *publication) { p.File.Identity.Key += bad }},
		{"fileRoot", "file", func(p *publication) { p.File.Identity.Root += bad }},
		{"fileSession", "file", func(p *publication) { p.File.Identity.SessionKey += bad }},
		{"sourcePath", "file", func(p *publication) { p.File.Sources[0].Path += bad }},
		{"originalSourcePath", "file", func(p *publication) { p.File.Sources[0].SourcePath = "/original/" + bad }},
		{"launchRoot", "launch", func(p *publication) { p.Launch.Root += bad }},
		{"launchPath", "launch", func(p *publication) { p.Launch.Envelope.Path += bad }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sy, _, _, bucket := contractArchive(t)
			p := branchPublication(tc.kind)
			if err := p.validate("machine"); err != nil {
				t.Fatalf("valid baseline = %v", err)
			}
			tc.mutate(p)
			_, err := sy.publish(t.Context(), p, nil)
			if err == nil || !strings.Contains(err.Error(), "non-UTF-8") {
				t.Fatalf("publish = %v, want explicit unsupported identity error", err)
			}
			if ops, _ := bucket.takeOps(); len(ops) != 0 {
				t.Fatalf("unsupported identity accessed destination: %v", ops)
			}
		})
	}
}

func TestUnsupportedUnixKeyAndRootNeverCreateUnrecoverableRecords(t *testing.T) {
	sy, cfg, _, bucket := contractArchive(t)
	path := filepath.Join(cfg.Home, "source")
	if err := os.WriteFile(path, []byte("valid bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := string([]byte{0xff})
	sf := adapters.SessionFile{Agent: "claude", Root: cfg.Home, Path: path, Key: "unsupported-" + bad, Role: adapters.RoleAux}
	sum, err := sy.Run(t.Context(), []adapters.SessionFile{sf})
	if err != nil || sum.Errors != 1 || !strings.Contains(strings.Join(sum.ErrorMessages, " "), "non-UTF-8") {
		t.Fatalf("Run unsupported key = %+v, %v", sum, err)
	}
	for key := range bucket.objects {
		if strings.HasPrefix(key, "v3/blobs/") || strings.Contains(key, "/records/") {
			t.Fatalf("unsupported key published %s", key)
		}
	}
	bucket.takeOps()
	if err := sy.rememberExtraRoot(t.Context(), "claude", "/tmp/"+bad); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("rememberExtraRoot = %v, want unsupported path error", err)
	}
	if ops, _ := bucket.takeOps(); len(ops) != 0 {
		t.Fatalf("unsupported extra root accessed destination: %v", ops)
	}
	roots, err := sy.db.ExtraRoots("claude")
	if err != nil || len(roots) != 0 {
		t.Fatalf("unsupported root cache = %+v, %v", roots, err)
	}
	if raw.ValidKey("unsupported-"+bad) || raw.ValidPath("/tmp/"+bad) || !raw.ValidPath("/tmp/café") {
		t.Fatal("UTF-8 identity grammar did not distinguish supported Unicode from Unix byte names")
	}
}

func TestSnapshotFilesystemLabelsRejectNonUTF8WithoutRenaming(t *testing.T) {
	bad := string([]byte{0xff})
	for _, mutate := range []func(*snapshot.Set){
		func(s *snapshot.Set) { s.UserBase += bad },
		func(s *snapshot.Set) { root := "/project/" + bad; s.ProjectRoot = &root },
		func(s *snapshot.Set) { s.Files[0].Path += bad },
		func(s *snapshot.Set) { s.Files[0].Target = "/target/" + bad },
		func(s *snapshot.Set) { s.Skipped = []snapshot.SkippedFile{{Path: "skipped-" + bad}} },
	} {
		set := &snapshot.Set{UserBase: "/user/café", Files: []snapshot.File{{Path: "skills/entrée.md"}}}
		if err := validateSnapshotPaths(set); err != nil {
			t.Fatal(err)
		}
		mutate(set)
		if err := validateSnapshotPaths(set); err == nil || !strings.Contains(err.Error(), "non-UTF-8") {
			t.Fatalf("snapshot labels = %v, want unsupported source path", err)
		}
	}
}
