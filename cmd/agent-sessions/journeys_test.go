package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // synthetic WAL fixture and independent restored-database queries

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/raw"
)

const acceptancePiName = "2026-10-10T10-00-00-000Z_11111111-2222-4333-8444-555555555555"

func acceptancePiBody(cwd string) []byte {
	return fmt.Appendf(nil, "{\"type\":\"session\",\"version\":3,\"id\":\"11111111-2222-4333-8444-555555555555\",\"cwd\":%q}\n", cwd)
}

func TestAcceptanceAppendHistoryAndIdle(t *testing.T) {
	r := newAcceptanceRun(t)
	key := "sessions/--acceptance--/" + acceptancePiName + ".jsonl"
	path := filepath.Join(r.home, ".pi", "agent", filepath.FromSlash(key))
	initial := append(acceptancePiBody(r.work), bytes.Repeat([]byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"base\"}}\n"), 1000)...)
	r.write(path, initial)
	r.cli("backfill", "--agent", "pi")
	base := r.latest("file", "pi", key)
	acknowledged := r.inventory()
	tail := []byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"tail\"}}\n")
	appended := append(bytes.Clone(initial), tail...)
	r.write(path, appended)
	r.cli("sync", "--agent", "pi", "--path", path)
	current := r.latest("file", "pi", key)
	if current.File.Kind != "append" {
		t.Errorf("append generation kind = %q, want append", current.File.Kind)
	}
	if got := current.File.Pieces; len(got) != len(base.File.Pieces)+1 || !slices.Equal(got[:len(got)-1], base.File.Pieces) {
		t.Fatalf("append pieces = %d, want original base plus one tail", len(got))
	}
	last := current.File.Pieces[len(current.File.Pieces)-1]
	if last.Start != int64(len(initial)) || last.End != int64(len(appended)) || last.SHA256 != raw.SHA256(tail) {
		t.Error("append tail coverage/hash differs from appended source bytes")
	}
	r.history("pi", key, [][]byte{initial, appended})
	r.retain(acknowledged)
	// Make the source idle without sleeping. This used to publish a whole-file
	// fold even when there was no new content.
	r.age(path)
	before := r.inventory()
	r.cli("sync", "--agent", "pi")
	r.unchanged(before)
	want := [][]byte{initial, appended}
	for _, body := range [][]byte{[]byte("{\"type\":\"session\",\"version\":3,\"id\":\"rewritten\"}\n"), {}} {
		r.write(path, body)
		r.cli("sync", "--agent", "pi", "--path", path)
		want = append(want, body)
	}
	acknowledged = r.inventory()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	want = append(want, []byte{}) // deletion retains the last (empty) content
	for range 2 {
		r.cold()
		r.cli("sync", "--agent", "pi")
		r.history("pi", key, want)
		if got := r.latest("file", "pi", key).File.Kind; got != "delete" {
			t.Errorf("cold current kind = %q, want delete", got)
		}
		r.retain(acknowledged)
	}
}

func TestAcceptanceAppendFoldRetainsFirstBase(t *testing.T) {
	r := newAcceptanceRun(t)
	key := "sessions/--acceptance--/" + acceptancePiName + ".jsonl"
	path := filepath.Join(r.home, ".pi", "agent", filepath.FromSlash(key))
	body := acceptancePiBody("") // no project snapshot: isolate append-chain behaviour
	r.write(path, body)
	r.cli("sync", "--agent", "pi")
	base := r.latest("file", "pi", key).File.Pieces[0]
	want := [][]byte{bytes.Clone(body)}
	previousCount, folded := 1, false
	for i := range 65 {
		body = fmt.Appendf(body, "{\"type\":\"message\",\"id\":%d}\n", i)
		r.write(path, body)
		r.cli("sync", "--agent", "pi", "--path", path)
		current := r.latest("file", "pi", key)
		pieces := current.File.Pieces
		if pieces[0] != base {
			t.Fatalf("first base replaced at append %d", i+1)
		}
		if len(pieces) < previousCount {
			folded = true
			if len(pieces) > 3 { // retained base + consolidated tail, optionally this append
				t.Errorf("folded pieces = %d, want retained base and consolidated tail", len(pieces))
			}
		}
		previousCount = len(pieces)
		want = append(want, bytes.Clone(body))
	}
	if !folded {
		t.Error("65 appends did not consolidate the 64-entry append chain")
	}
	r.history("pi", key, want)
	acknowledged := r.inventory()
	r.cold()
	r.cli("sync", "--agent", "pi")
	r.history("pi", key, want)
	r.retain(acknowledged)
}

func TestAcceptancePiChildAndLateMetadata(t *testing.T) {
	r := newAcceptanceRun(t)
	root := filepath.Join(r.home, ".pi", "agent")
	key := "sessions/--acceptance--/" + acceptancePiName + ".jsonl"
	path := filepath.Join(root, filepath.FromSlash(key))
	body := append(acceptancePiBody(r.work), []byte("{\"type\":\"model_change\",\"provider\":\"synthetic\",\"modelId\":\"early-model\"}\n")...)
	body = append(body, bytes.Repeat([]byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"padding\"}}\n"), 300)...)
	r.write(path, body)
	childKey := "sessions/--acceptance--/" + acceptancePiName + "/tasks/child.jsonl"
	child := []byte("{\"type\":\"session\",\"version\":3,\"id\":\"child\",\"cwd\":\"\"}\n")
	r.write(filepath.Join(root, filepath.FromSlash(childKey)), child)
	r.cli("backfill", "--agent", "pi")
	body = append(body, []byte("{\"type\":\"model_change\",\"provider\":\"synthetic\",\"modelId\":\"late-model\"}\n")...)
	r.write(path, body)
	r.cli("sync", "--agent", "pi", "--path", path)
	parent := r.latest("file", "pi", key)
	childRecord := r.latest("file", "pi", childKey)
	if childRecord.File.Role != "subagent" || childRecord.File.Identity.SessionKey != parent.File.Identity.SessionKey {
		t.Errorf("child role/session = %q/%q, want subagent/%q", childRecord.File.Role, childRecord.File.Identity.SessionKey, parent.File.Identity.SessionKey)
	}
	if !bytes.Equal(r.content(childRecord), child) {
		t.Error("child transcript bytes differ")
	}
	manifest := r.latest("manifest", "pi", parent.File.Identity.SessionKey).Manifest
	found := slices.ContainsFunc(manifest.Transcripts, func(transcript struct {
		Key  string `json:"key"`
		Role string `json:"role"`
	},
	) bool {
		return transcript.Key == childKey && transcript.Role == "subagent"
	})
	if !found {
		t.Error("parent manifest does not include child as subagent")
	}
	if got := manifest.Info["model"]; got != "late-model" {
		t.Errorf("manifest model = %v, want late-model from appended tail", got)
	}
	before := r.inventory()
	r.cold()
	r.cli("sync", "--agent", "pi")
	recovered := r.latest("manifest", "pi", parent.File.Identity.SessionKey).Manifest
	if diff := cmp.Diff(manifest.Info, recovered.Info); diff != "" {
		t.Errorf("cold manifest info differs (-want +got):\n%s", diff)
	}
	r.retain(before)
}

func TestAcceptanceLiteralGuardAndProvenance(t *testing.T) {
	r := newAcceptanceRun(t)
	root := filepath.Join(r.home, ".pi", "agent")
	literals := []string{"acceptance-fake-shell-literal-0001", "acceptance-fake-fish-literal-0002", "acceptance-fake-snapshot-literal-0003"}
	r.write(filepath.Join(root, "extensions", "unsafe.sh"), []byte("export API_TOKEN='"+literals[0]+"'\n"))
	r.write(filepath.Join(root, "skills", "unsafe.fish"), []byte("set -gx SERVICE_SECRET \""+literals[1]+"\"\n"))
	r.write(filepath.Join(r.work, ".pi", "extensions", "snapshot.sh"), []byte("export DB_PASSWORD="+literals[2]+"\n"))
	reference := []byte("export API_TOKEN=\"$API_TOKEN\"\nset -gx SERVICE_SECRET $SERVICE_SECRET\n")
	r.write(filepath.Join(root, "extensions", "references.sh"), reference)
	provenance := []byte("// synthetic installed extension provenance\nexport const source = 'acceptance';\n")
	for _, key := range []string{"git/example/source.ts", "npm/example/source.ts"} {
		r.write(filepath.Join(root, filepath.FromSlash(key)), provenance)
	}
	noise := []string{"Example.app/source.txt", "stats_cache.json", "stats-cache.json.bak", ".codex-global-state.json", "logs_1.sqlite"}
	for _, key := range noise {
		path := filepath.Join(root, filepath.FromSlash(key))
		r.write(path, []byte("acceptance-noise-never-capture"))
		if strings.HasSuffix(key, ".sqlite") {
			r.write(path, nil)
			db := acceptanceOpenSQLite(t, path)
			if _, err := db.ExecContext(t.Context(), "CREATE TABLE noise (value TEXT); INSERT INTO noise VALUES ('acceptance-noise-never-capture')"); err != nil {
				t.Fatal(err)
			}
		}
	}
	key := "sessions/--acceptance--/" + acceptancePiName + ".jsonl"
	// This guard is deliberately not transcript DLP.
	transcript := append(acceptancePiBody(r.work), []byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"export API_TOKEN=transcript-literal-control\"}}\n")...)
	r.write(filepath.Join(root, filepath.FromSlash(key)), transcript)
	r.cli("backfill", "--agent", "pi")
	r.absentLiterals(append(literals, "acceptance-noise-never-capture")...)
	for _, key := range noise {
		if len(r.records("file", "pi", key)) != 0 || len(r.records("file", "pi", "db/"+key)) != 0 {
			t.Errorf("noise file acquired a committed generation: %s", key)
		}
	}
	for _, key := range []string{"git/example/source.ts", "npm/example/source.ts"} {
		if !bytes.Equal(r.content(r.latest("file", "pi", key)), provenance) {
			t.Errorf("provenance bytes differ: %s", key)
		}
	}
	if !bytes.Equal(r.content(r.latest("file", "pi", "extensions/references.sh")), reference) {
		t.Error("environment references were withheld or altered")
	}
	if !bytes.Equal(r.content(r.latest("file", "pi", key)), transcript) {
		t.Error("transcript literal control was withheld or altered")
	}
}

func TestAcceptanceRegularSymlinkCapture(t *testing.T) {
	r := newAcceptanceRun(t)
	root := filepath.Join(r.home, ".pi", "agent")
	body := []byte("safe linked extension source\n")
	target := filepath.Join(r.work, "linked-source.ts")
	r.write(target, body)
	link := filepath.Join(root, "extensions", "linked.ts")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	r.cli("sync", "--agent", "pi")
	if !bytes.Equal(r.content(r.latest("file", "pi", "extensions/linked.ts")), body) {
		t.Error("regular symlink target bytes differ")
	}
}

func TestAcceptanceSQLiteChangedChunksAndWAL(t *testing.T) {
	r := newAcceptanceRun(t)
	path := filepath.Join(r.home, ".codex", "state_5.sqlite")
	r.write(path, nil) // SQLite itself must not create a world-readable fixture.
	db := acceptanceOpenSQLite(t, path)
	for _, stmt := range []string{
		"PRAGMA page_size=4096", "PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE entries (id INTEGER PRIMARY KEY, value BLOB)",
		"WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<2304) INSERT INTO entries SELECT i, randomblob(3900) FROM n",
	} {
		if _, err := db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	key := "db/state_5.sqlite"
	r.cli("backfill", "--agent", "codex")
	base := r.latest("file", "codex", key)
	if base.File.Size < 8<<20 || len(base.File.Pieces) < 8 {
		t.Fatalf("database size/pieces = %d/%d, want >=8 MiB in >=8 chunks", base.File.Size, len(base.File.Pieces))
	}
	for i, piece := range base.File.Pieces {
		if piece.Start != int64(i)*(1<<20) || piece.End-piece.Start > 1<<20 || i < len(base.File.Pieces)-1 && piece.End-piece.Start != 1<<20 {
			t.Errorf("database piece %d does not follow fixed 1 MiB boundaries", i)
		}
	}
	baseBody := r.content(base)
	acceptanceCheckSQLite(t, r, baseBody, nil)
	acknowledged := r.inventory()
	value := bytes.Repeat([]byte("v"), 3900)
	if _, err := db.ExecContext(t.Context(), "UPDATE entries SET value=? WHERE id=1700", value); err != nil {
		t.Fatal(err)
	}
	// Only age private export mtimes to make the documented recopy interval due.
	count := 0
	err := filepath.WalkDir(filepath.Join(r.state, "exports"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "state_5.sqlite" {
			r.age(path)
			count++
		}
		return nil
	})
	if err != nil || count != 1 {
		t.Fatalf("private exports = %d, want 1: %v", count, err)
	}
	r.cli("sync", "--agent", "codex")
	current := r.latest("file", "codex", key)
	afterBody := r.content(current)
	acceptanceCheckSQLite(t, r, afterBody, value)
	acceptanceCheckChunkDelta(t, r, base.File.Pieces, current.File.Pieces, acknowledged)
	r.history("codex", key, [][]byte{baseBody, afterBody})
	// No source remains to mask a broken remote-only recovery.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r.cold()
	r.cli("sync", "--agent", "codex")
	r.history("codex", key, [][]byte{baseBody, afterBody, afterBody})
	r.retain(acknowledged)
}

func acceptanceOpenSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func acceptanceCheckSQLite(t *testing.T, r *acceptanceRun, body, want []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	r.write(path, body)
	db := acceptanceOpenSQLite(t, path)
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries").Scan(&count); err != nil || count != 2304 {
		t.Fatalf("restored row count = %d, want 2304: %v", count, err)
	}
	var check string
	if err := db.QueryRowContext(t.Context(), "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("restored quick_check = %q, want ok: %v", check, err)
	}
	if want != nil {
		var got []byte
		if err := db.QueryRowContext(t.Context(), "SELECT value FROM entries WHERE id=1700").Scan(&got); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("restored WAL page edit differs: %v", err)
		}
	}
}

func acceptanceCheckChunkDelta(t *testing.T, r *acceptanceRun, before, after []raw.Piece, inventory map[string]string) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("edited database chunks = %d, want %d", len(after), len(before))
	}
	changed := 0
	fresh := map[string]bool{}
	for i, piece := range after {
		if piece.Start != before[i].Start || piece.End != before[i].End {
			t.Errorf("edited database piece %d moved boundaries", i)
		}
		if piece.SHA256 != before[i].SHA256 {
			changed++
			fresh[piece.Object] = true
		} else if piece != before[i] {
			t.Errorf("unchanged database piece %d did not reuse its object", i)
		}
	}
	if changed == 0 || changed > 2 {
		t.Errorf("changed chunks = %d, want 1 or 2 (SQLite header and edited page)", changed)
	}
	for key := range r.inventory() {
		if strings.HasPrefix(key, "v3/blobs/") && inventory[key] == "" && !fresh[key] {
			t.Errorf("page edit uploaded an unrelated new blob: %s", key)
		}
	}
	r.retain(inventory)
}

func TestAcceptanceGuardReclassificationRetainsHistory(t *testing.T) {
	r := newAcceptanceRun(t)
	root := filepath.Join(r.home, ".pi", "agent")
	key := "extensions/reclassified.sh"
	path := filepath.Join(root, filepath.FromSlash(key))
	safe := []byte("export API_TOKEN=\"$API_TOKEN\"\n")
	r.write(path, safe)
	r.cli("sync", "--agent", "pi")
	acknowledged := r.inventory()
	literal := "acceptance-fake-reclassified-literal-0004"
	r.write(path, []byte("export API_TOKEN='"+literal+"'\n"))
	control := []byte("ordinary sibling still captured\n")
	r.write(filepath.Join(root, "sibling.txt"), control)
	r.cli("sync", "--agent", "pi")
	r.history("pi", key, [][]byte{safe})
	if !bytes.Equal(r.content(r.latest("file", "pi", "sibling.txt")), control) {
		t.Error("allowed sibling not captured during withholding")
	}
	r.absentLiterals(literal)
	r.retain(acknowledged)
	r.cold()
	r.cli("sync", "--agent", "pi")
	r.history("pi", key, [][]byte{safe})
	r.absentLiterals(literal)
}
