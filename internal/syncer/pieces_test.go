package syncer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

type pieceFixture struct {
	sy     *Syncer
	sf     adapters.SessionFile
	bucket *memStore
}

func newPieceFixture(t *testing.T, name string, role adapters.Role) *pieceFixture {
	t.Helper()
	sy, bucket := branchSyncer(t)
	sf := adapters.SessionFile{Agent: "claude", Root: sy.cfg.Home, Path: filepath.Join(sy.cfg.Home, name), Key: name, Role: role}
	return &pieceFixture{sy: sy, sf: sf, bucket: bucket}
}

func (f *pieceFixture) run(t *testing.T, data []byte) (*publication, *Summary) {
	t.Helper()
	if data != nil {
		if err := os.WriteFile(f.sf.Path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(f.sf.Path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	f.sy = New(f.sy.cfg, f.bucket, f.sy.db, f.sy.adapters, Options{})
	sum, err := f.sy.Run(t.Context(), []adapters.SessionFile{f.sf})
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Run = %+v, %v", sum, err)
	}
	p, _, err := f.sy.current("file", f.sf.Agent, f.sf.Key)
	if err != nil {
		t.Fatal(err)
	}
	return p, sum
}

func TestAppendRequiresCompleteJSONLParentButNotBinaryNewline(t *testing.T) {
	for _, tc := range []struct{ name, before, after, kind string }{
		{"complete.jsonl", "a\n", "a\nb\n", "append"},
		{"fragment.jsonl", "a", "ab\n", "capture"},
		{"binary.dat", "\x00a", "\x00ab", "append"},
		{"rewrite.jsonl", "a\n", "z\nb\n", "capture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPieceFixture(t, tc.name, adapters.RoleAux)
			first, _ := f.run(t, []byte(tc.before))
			p, _ := f.run(t, []byte(tc.after))
			if p.File.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", p.File.Kind, tc.kind)
			}
			if tc.kind == "append" && p.File.Pieces[0] != first.File.Pieces[0] {
				t.Fatal("append lost immutable prefix")
			}
			var out bytes.Buffer
			if err := f.sy.Restore(t.Context(), f.sf.Agent, f.sf.Key, "", &out); err != nil || out.String() != tc.after {
				t.Fatalf("Restore = %q, %v", out.String(), err)
			}
		})
	}
}

func TestAppendBoundaryFoldRetainsBaseAndEveryHistoricalVersion(t *testing.T) {
	f := newPieceFixture(t, "transcript.jsonl", adapters.RoleAux)
	body := []byte("base\n")
	base, _ := f.run(t, body)
	for i := 1; i < 63; i++ {
		body = fmt.Appendf(body, "line %d\n", i)
		f.run(t, body)
	}
	p, sum := f.run(t, nil)
	if len(p.File.Pieces) != 63 || sum.Uploaded != 0 {
		t.Fatalf("63 boundary = %d pieces, %+v", len(p.File.Pieces), sum)
	}
	body = append(body, []byte("line 63\n")...)
	p, _ = f.run(t, body)
	if len(p.File.Pieces) != 64 || p.File.Kind != "append" {
		t.Fatalf("64 boundary = %+v", p.File)
	}
	p, sum = f.run(t, nil)
	if sum.Folded != 1 || len(p.File.Pieces) != 2 || p.File.Pieces[0] != base.File.Pieces[0] || p.File.SHA256 != raw.SHA256(body) {
		t.Fatalf("fold = %+v, %+v", p.File, sum)
	}
	records, err := f.sy.db.ArchiveRecords("file", streamID(f.sf.Agent, f.sf.Key))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if !r.Committed {
			continue
		}
		var out bytes.Buffer
		if err := f.sy.Restore(t.Context(), f.sf.Agent, f.sf.Key, r.ID, &out); err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(body, out.Bytes()) {
			t.Fatal("historical bytes lost")
		}
	}
	body = append(body, []byte("after idle\n")...)
	p, sum = f.run(t, body)
	if p.File.Kind != "append" || sum.Segments != 1 || p.File.Pieces[0] != base.File.Pieces[0] {
		t.Fatalf("append after fold = %+v, %+v", p.File, sum)
	}
}

func TestContinuousGrowthFoldsAcknowledgedParentBeforeAppending(t *testing.T) {
	f := newPieceFixture(t, "file", adapters.RoleAux)
	body := []byte("base")
	first, _ := f.run(t, body)
	folds := 0
	for i := 1; i <= 130; i++ {
		body = append(body, byte(i))
		p, sum := f.run(t, body)
		folds += sum.Folded
		if p.File.Kind != "append" || sum.Segments != 1 || len(p.File.Pieces) > 64 || p.File.Pieces[0] != first.File.Pieces[0] {
			t.Fatalf("growth %d = %+v, %+v", i, p.File, sum)
		}
		if sum.Folded > 0 {
			records, err := f.sy.db.ArchiveRecords("file", streamID(f.sf.Agent, f.sf.Key))
			if err != nil {
				t.Fatal(err)
			}
			var committed []state.Record
			for _, r := range records {
				if r.Committed {
					committed = append(committed, r)
				}
			}
			fold, err := decodePublication(committed[len(committed)-2].Body)
			if err != nil {
				t.Fatal(err)
			}
			if fold.File.Kind != "fold" || fold.File.SHA256 != raw.SHA256(body[:len(body)-1]) || p.Parent != committed[len(committed)-2].ID {
				t.Fatal("fold included unacknowledged growing bytes or broke parent chain")
			}
			var out bytes.Buffer
			if err := f.sy.Restore(t.Context(), f.sf.Agent, f.sf.Key, committed[len(committed)-2].ID, &out); err != nil || !bytes.Equal(out.Bytes(), body[:len(body)-1]) {
				t.Fatalf("fold Restore = %v", err)
			}
		}
	}
	if folds != 2 {
		t.Fatalf("continuous folds = %d, want 2", folds)
	}
}

func TestDatabaseChunksReuseAnywhereAndRestoreAfterColdRecovery(t *testing.T) {
	f := newPieceFixture(t, "database.sqlite", adapters.RoleDB)
	block := bytes.Repeat([]byte("a"), 1<<20)
	initial := bytes.Repeat(block, 8)
	first, firstSum := f.run(t, initial)
	if firstSum.BytesUploaded != first.File.Pieces[0].EncodedSize {
		t.Fatal("repeated blocks uploaded more than once")
	}
	if len(first.File.Pieces) != 8 {
		t.Fatalf("pieces = %d", len(first.File.Pieces))
	}
	for _, p := range first.File.Pieces {
		if p.Object != first.File.Pieces[0].Object {
			t.Fatal("identical blocks have different identities")
		}
	}
	changed := bytes.Clone(initial)
	changed[100] = 1 // a header/page-sized change only affects the first chunk
	second, secondSum := f.run(t, changed)
	if secondSum.BytesUploaded != second.File.Pieces[0].EncodedSize {
		t.Fatal("header update reuploaded unchanged chunks")
	}
	if second.File.Pieces[0].Object == first.File.Pieces[0].Object || second.File.Pieces[1].Object != first.File.Pieces[1].Object {
		t.Fatal("single-page update did not reuse other chunks")
	}
	inserted := append(bytes.Repeat([]byte("b"), 1<<20), changed...)
	third, thirdSum := f.run(t, inserted)
	if thirdSum.BytesUploaded != third.File.Pieces[0].EncodedSize {
		t.Fatal("offset reuse reuploaded shifted chunks")
	}
	if third.File.Pieces[1].Object != second.File.Pieces[0].Object {
		t.Fatal("shifted block was not reused at a different offset")
	}
	truncated := bytes.Clone(inserted[:7<<20])
	f.run(t, truncated)
	large := bytes.Repeat(block, 65)
	p, _ := f.run(t, large)
	_, sum := f.run(t, nil)
	if len(p.File.Pieces) != 65 || sum.Folded != 0 || sum.Uploaded != 0 {
		t.Fatal("database folded by piece count")
	}
	records, err := f.sy.db.ArchiveRecords("file", streamID(f.sf.Agent, f.sf.Key))
	if err != nil {
		t.Fatal(err)
	}
	// A fresh disposable cache, with no source dependency, exercises derived dependencies.
	db, err := state.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	cold := New(f.sy.cfg, f.bucket, db, f.sy.adapters, Options{})
	if err := cold.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{initial, changed, inserted, truncated, large}
	i := 0
	for _, r := range records {
		if !r.Committed {
			continue
		}
		var out bytes.Buffer
		if err := cold.Restore(t.Context(), f.sf.Agent, f.sf.Key, r.ID, &out); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), want[i]) {
			t.Fatalf("version %d differs", i)
		}
		i++
	}
}

func TestDatabaseChunkThresholdAndCancellationCleanup(t *testing.T) {
	f := newPieceFixture(t, "database.sqlite", adapters.RoleDB)
	p, _ := f.run(t, bytes.Repeat([]byte("x"), 7<<20))
	if len(p.File.Pieces) != 1 {
		t.Fatal("small database chunked")
	}
	p, _ = f.run(t, bytes.Repeat([]byte("x"), 8<<20))
	if len(p.File.Pieces) != 8 || p.File.Kind != "capture" {
		t.Fatal("threshold growth was not a chunked capture")
	}
	u, err := f.sy.prepare(t.Context(), f.sf)
	if err != nil {
		t.Fatal(err)
	}
	if u != nil {
		u.cleanup()
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.sy.stageDecoded(ctx, f.sf); err == nil {
		t.Fatal("canceled staging succeeded")
	}
	entries, err := os.ReadDir(f.sy.tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging leftovers = %v", entries)
	}
}

func TestDerivedFileDependenciesPreserveLegacyValidation(t *testing.T) {
	p := branchPublication("file")
	legacy, _, err := canonicalPublication(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := decodePublication(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.validate("machine"); err != nil {
		t.Fatal(err)
	}
	p.Dependencies = nil
	body, _, err := canonicalPublication(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(`"dependencies"`)) {
		t.Fatal("file serialized duplicate dependencies")
	}
	parsed, err = decodePublication(body)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(parsed.dependencies(), []dependency{pieceDependency(p.File.Pieces[0])}) {
		t.Fatal("derived dependency differs")
	}
	for _, bad := range []string{`null`, `[]`, `[{"object":"forged"}]`} {
		forged := strings.TrimSuffix(string(body), "}") + `,"dependencies":` + bad + `}`
		parsed, err := decodePublication([]byte(forged))
		if err == nil {
			err = parsed.validate("machine")
		}
		if err == nil {
			t.Fatalf("accepted supplied dependencies %s", bad)
		}
	}
}

func TestFileTransitionRejectsForgedAppendAndChangedFold(t *testing.T) {
	f := newPieceFixture(t, "file", adapters.RoleAux)
	first, _ := f.run(t, []byte("a"))
	child, _ := f.run(t, []byte("ab"))
	if err := validateFileTransition(child, first.File); err != nil {
		t.Fatal(err)
	}
	altered := *child
	g := *child.File
	altered.File = &g
	g.Pieces = slices.Clone(g.Pieces)
	g.Pieces[0].SHA256 = raw.SHA256([]byte("z"))
	if err := validateFileTransition(&altered, first.File); err == nil {
		t.Fatal("accepted forged inherited prefix")
	}
	g.Kind = "fold"
	if err := validateFileTransition(&altered, first.File); err == nil {
		t.Fatal("accepted changed-content fold")
	}
}

func TestMetadataOnlyReclassificationReusesPiecesAndMovesManifestMembership(t *testing.T) {
	f := newPieceFixture(t, "file", adapters.RoleAux)
	f.sf.SessionKey = "old"
	first, _ := f.run(t, []byte("content"))
	f.sf.Role = adapters.RoleUnknown
	f.sf.SessionKey = "new"
	p, sum := f.run(t, nil)
	if p.File.Kind != "capture" || p.Parent == "" || sum.BytesUploaded != 0 || !slices.Equal(p.File.Pieces, first.File.Pieces) {
		t.Fatalf("metadata capture = %+v, %+v", p.File, sum)
	}
	old, _, err := f.sy.current("manifest", f.sf.Agent, "old")
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := f.sy.current("manifest", f.sf.Agent, "new")
	if err != nil {
		t.Fatal(err)
	}
	if old == nil || len(old.Manifest.Transcripts) != 0 || next == nil || len(next.Manifest.Transcripts) != 1 || next.Manifest.Transcripts[0].Role != adapters.RoleUnknown {
		t.Fatal("manifest membership did not move")
	}
}
