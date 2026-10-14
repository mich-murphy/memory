package syncer_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/mich-murphy/memory/internal/cli"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/syncer"
	"github.com/mich-murphy/memory/internal/testenv"
)

const (
	claudeSID   = testenv.ClaudeSID
	codexThread = testenv.CodexThread
	slug        = testenv.Slug
	claudeKey   = testenv.ClaudeKey
	codexKey    = testenv.CodexKey
	piKey       = testenv.PiKey
)

var rawKey = testenv.Raw

// run runs the command line in-process and returns its exit code and stdout.
func run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Main(t.Context(), args, "test", cli.IO{Stdout: &out, Stderr: &errOut})
	if errOut.Len() > 0 {
		t.Logf("stderr: %s", errOut.String())
	}
	return code, out.String()
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out := run(t, args...)
	if code != 0 {
		t.Fatalf("agent-sessions %v: exit %d", args, code)
	}
	return out
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// readObject rebuilds a raw file without its manifest, as docs/storage-layout.md says a reader
// can: the base object, then each segment that starts where the bytes so far end.
func readObject(t *testing.T, e *testenv.Env, key string) []byte {
	t.Helper()
	out := readBlob(t, e, key)
	segments, err := os.ReadDir(filepath.Join(e.Bucket, filepath.FromSlash(key)+".d"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, s := range segments {
		startText, _, _ := strings.Cut(s.Name(), "-")
		start, err := strconv.ParseInt(startText, 10, 64)
		if err != nil {
			t.Fatalf("segment %s: %v", s.Name(), err)
		}
		if start == int64(len(out)) {
			out = append(out, readBlob(t, e, key+".d/"+s.Name())...)
		}
	}
	return out
}

func readBlob(t *testing.T, e *testenv.Env, key string) []byte {
	t.Helper()
	data, err := raw.Decompress(readFile(t, filepath.Join(e.Bucket, key)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func toPieces(t *testing.T, v any) []raw.Piece {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var pieces []raw.Piece
	if err := json.Unmarshal(b, &pieces); err != nil {
		t.Fatal(err)
	}
	return pieces
}

func readPieces(t *testing.T, e *testenv.Env, v any) []byte {
	t.Helper()
	var out []byte
	for _, p := range toPieces(t, v) {
		if p.Start != int64(len(out)) {
			t.Fatalf("gap or overlap at byte %d: next piece starts at %d", len(out), p.Start)
		}
		data := readBlob(t, e, p.Object)
		if int64(len(data)) != p.End-p.Start || raw.SHA256(data) != p.SHA256 {
			t.Fatalf("%s does not match its sha256 or length", p.Object)
		}
		out = append(out, data...)
	}
	return out
}

func mainTranscript(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	for _, f := range m["transcripts"].([]any) {
		if tr := f.(map[string]any); tr["role"] == "main" {
			return tr
		}
	}
	t.Fatal("no main transcript")
	return nil
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func mtimes(t *testing.T, e *testenv.Env) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, k := range e.BucketKeys() {
		st, err := os.Stat(filepath.Join(e.Bucket, k))
		if err != nil {
			t.Fatal(err)
		}
		out[k] = st.ModTime().UnixNano()
	}
	return out
}

func hasPrefix(keys []string, prefixes ...string) bool {
	return slices.ContainsFunc(keys, func(k string) bool {
		return slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(k, p) })
	})
}

func TestSweepCapturesEveryAgentDirAndIsIdempotent(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	p := "projects/" + slug
	for _, k := range []string{
		rawKey("claude", p+"/"+claudeSID+".jsonl"),
		rawKey("claude", p+"/"+claudeSID+"/subagents/agent-abc.jsonl"),
		rawKey("claude", p+"/"+claudeSID+"/subagents/agent-abc.meta.json"),
		rawKey("claude", p+"/"+claudeSID+"/tool-results/toolu_1.txt"),
		rawKey("claude", p+"/"+claudeSID+".jsonl.superseded-20261001"),
		rawKey("claude", p+"/memory/MEMORY.md"),
		rawKey("claude", "plans/brave-plan.md"),
		rawKey("claude", "history.jsonl"),
		rawKey("claude", "settings.json"),
		rawKey("claude", "skills/deploy/scripts/go.sh"),
		rawKey("claude", "file-history/"+claudeSID+"/abc@v1"),
		rawKey("claude", "teams/alpha/inboxes/lead.json"),
		rawKey("claude", "sessions/123.json"),
		rawKey("claude", "mystery.dat"),
		rawKey("claude", "claude.json"),
		rawKey("codex", "rollouts/"+codexKey+".jsonl"),
		rawKey("codex", "history.jsonl"),
		rawKey("codex", "config.toml"),
		rawKey("codex", "memories/notes.md"),
		rawKey("pi", "sessions/--home-code-proj--/"+piKey+".jsonl"),
		rawKey("pi", "AGENTS.md"),
		"manifests/agent=claude/machine=testbox/" + claudeKey + ".json",
		"manifests/agent=codex/machine=testbox/" + codexKey + ".json",
		"manifests/agent=pi/machine=testbox/" + piKey + ".json",
	} {
		if !e.HasKey(k) {
			t.Errorf("missing %s", k)
		}
	}
	original := readFile(t, filepath.Join(e.ClaudeSlug, claudeSID+".jsonl"))
	if got := readObject(t, e, rawKey("claude", p+"/"+claudeSID+".jsonl")); !bytes.Equal(got, original) {
		t.Errorf("stored transcript differs:\n%s", cmp.Diff(string(original), string(got)))
	}

	m := e.Manifest("claude", claudeKey)
	info := m["info"].(map[string]any)
	if m["schema"] != 2.0 || m["machine_id"] != "testbox" || m["hostname"] == "" {
		t.Errorf("manifest header: schema %v machine %v hostname %v", m["schema"], m["machine_id"], m["hostname"])
	}
	if info["session_id"] != claudeSID || info["cwd"] != e.Project {
		t.Errorf("info = %v", info)
	}
	roles := map[string]bool{}
	for _, f := range list(m["transcripts"]) {
		roles[f.(map[string]any)["role"].(string)] = true
	}
	for _, r := range []string{"main", "subagent", "tool_result", "superseded", "file_history"} {
		if !roles[r] {
			t.Errorf("no %s transcript in %v", r, roles)
		}
	}
	main := mainTranscript(t, m)
	if main["line_count"] != 2.0 || main["prefix_sha256"] == nil || main["head"].(map[string]any)["session_id"] != claudeSID {
		t.Errorf("main transcript = %v", main)
	}
	if len(list(main["versions"])) != 0 {
		t.Errorf("versions = %v", main["versions"])
	}
	snap := list(m["snapshots"])[0].(map[string]any)
	if snap["timing"] != "post_hoc" || snap["git"].(map[string]any)["branch"] != "main" {
		t.Errorf("snapshot = %v", snap)
	}
	git := m["git"].(map[string]any)
	if git["head"] == nil || git["remote"] != "https://github.com/me/proj.git" {
		t.Errorf("git = %v", git)
	}

	codex := e.Manifest("codex", codexKey)["info"].(map[string]any)
	if codex["session_id"] != codexThread || codex["git_branch"] != "main" || codex["agent_version"] != "0.162.1" {
		t.Errorf("codex info = %v", codex)
	}
	pi := e.Manifest("pi", piKey)["info"].(map[string]any)
	if pi["session_id"] != testenv.PiSID || pi["format_version"] != 3.0 {
		t.Errorf("pi info = %v", pi)
	}

	before := mtimes(t, e)
	if out := mustRun(t, "sync"); !strings.Contains(out, "uploaded 0 files") {
		t.Errorf("second sync: %q", out)
	}
	if diff := cmp.Diff(before, mtimes(t, e)); diff != "" {
		t.Errorf("second sync rewrote objects:\n%s", diff)
	}
}

func decodeAny(t *testing.T, path string) []byte {
	t.Helper()
	data := readFile(t, path)
	if filepath.Ext(path) != ".zst" {
		return data
	}
	out, err := raw.Decompress(data)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func lastRunSummary(t *testing.T, e *testenv.Env) map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(e.State, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var summary string
	if err := db.QueryRowContext(t.Context(), "SELECT summary FROM runs ORDER BY id DESC LIMIT 1").Scan(&summary); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(summary), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCredentialsAndNoiseAreNeverCopied(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	keys := e.BucketKeys()
	for _, k := range keys {
		for _, bad := range []string{".credentials.json.zst", "auth.json.zst", ".key.zst"} {
			if strings.HasSuffix(k, bad) {
				t.Errorf("copied %s", k)
			}
		}
		if strings.Contains(k, "shell-snapshots") {
			t.Errorf("copied %s", k)
		}
	}
	var everything []byte
	for _, k := range keys {
		everything = append(everything, decodeAny(t, filepath.Join(e.Bucket, k))...)
	}
	credentials := []string{"secret-oauth", "secret-key", "sk-ant-api-secret", "ghp_token"}
	configSecrets := []string{"sk-ant-xxxx", "ghp_aaaa", "Bearer abc", "hunter2", "ghp_secret", "sk-pi-literal"}
	for _, secret := range slices.Concat(credentials, configSecrets) {
		if bytes.Contains(everything, []byte(secret)) {
			t.Errorf("bucket contains %q", secret)
		}
	}
	keysAndSettingsBesideSecrets := []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "DB_PASSWORD", "https://llm.corp/v1", "guard.sh"}
	for _, kept := range keysAndSettingsBesideSecrets {
		if !bytes.Contains(everything, []byte(kept)) {
			t.Errorf("bucket lacks %q", kept)
		}
	}
	var claudeJSON map[string]any
	if err := json.Unmarshal(readObject(t, e, rawKey("claude", "claude.json")), &claudeJSON); err != nil {
		t.Fatal(err)
	}
	if claudeJSON["primaryApiKey"] != scrub.Redacted {
		t.Errorf("claude.json primaryApiKey = %v", claudeJSON["primaryApiKey"])
	}
	if claudeJSON["oauthAccount"].(map[string]any)["emailAddress"] != "me@example.com" {
		t.Errorf("claude.json lost oauthAccount: %v", claudeJSON)
	}
	if n := lastRunSummary(t, e)["unknown"].(float64); n < 1 {
		t.Errorf("unknown files counted = %v, want at least 1", n)
	}
}

// TestTheCollectorsOwnConfigIsNeverCopied keeps the config file, which holds the R2
// credentials, in an agent's directory.
func TestTheCollectorsOwnConfigIsNeverCopied(t *testing.T) {
	e := testenv.New(t)
	cfg := filepath.Join(e.Claude, "agent-sessions.env")
	testenv.Write(t, cfg, "R2_SECRET_ACCESS_KEY=not-a-real-secret\n")
	t.Setenv("AGENT_SESSIONS_CONFIG", cfg)
	mustRun(t, "sync", "--quiet")
	if e.HasKey(rawKey("claude", "agent-sessions.env")) {
		t.Error("copied the collector's config")
	}
}

func TestAppendedSessionUploadsOnlyNewBytesThenFolds(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := rawKey("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	first := int64(len(readFile(t, path)))
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "more"}}))
	testenv.Backdate(t, path, 2*time.Minute) // written two minutes ago: the session is still going
	mustRun(t, "sync", "--quiet")
	now := readFile(t, path)
	segment := fmt.Sprintf("%s.d/%015d-%015d.zst", key, first, len(now)) // docs/storage-layout.md
	if !e.HasKey(segment) {
		t.Fatalf("no segment %s in %v", segment, e.BucketKeys())
	}
	if got := readBlob(t, e, segment); !bytes.Equal(got, now[first:]) {
		t.Errorf("segment holds %q", got)
	}
	if !bytes.Equal(readObject(t, e, key), now) {
		t.Error("rebuilt object differs")
	}
	main := mainTranscript(t, e.Manifest("claude", claudeKey))
	if main["size"] != float64(len(now)) || main["line_count"] != 3.0 || len(list(main["versions"])) != 0 {
		t.Errorf("main = %v", main)
	}
	if starts := toPieces(t, main["pieces"]); len(starts) != 2 || starts[0].Start != 0 || starts[1].Start != first {
		t.Errorf("pieces = %v", starts)
	}
	if !bytes.Equal(readPieces(t, e, main["pieces"]), now) {
		t.Error("pieces don't rebuild the file")
	}
	if hasPrefix(e.BucketKeys(), "raw-versions/", "blobs/") {
		t.Error("an append kept a version")
	}

	testenv.Backdate(t, path, syncer.FoldAfter+time.Minute)
	mustRun(t, "sync", "--quiet")
	if hasPrefix(e.BucketKeys(), key+".d/") {
		t.Error("segments left after folding")
	}
	if !bytes.Equal(readBlob(t, e, key), now) {
		t.Error("folded object differs")
	}
	main = mainTranscript(t, e.Manifest("claude", claudeKey))
	want := []raw.Piece{{Start: 0, End: int64(len(now)), SHA256: main["sha256"].(string), Object: key}}
	if diff := cmp.Diff(want, toPieces(t, main["pieces"])); diff != "" {
		t.Errorf("pieces after folding:\n%s", diff)
	}
	before := mtimes(t, e)[key]
	mustRun(t, "sync", "--quiet")
	if mtimes(t, e)[key] != before {
		t.Error("folded object rewritten")
	}
}

// TestAChangeToASessionsOtherFilesRefreshesItsManifest changes only a tool result, which has
// no head to read.
func TestAChangeToASessionsOtherFilesRefreshesItsManifest(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	const text = "bigger output\n"
	testenv.Write(t, filepath.Join(e.ClaudeSlug, claudeSID, "tool-results", "toolu_1.txt"), text)
	mustRun(t, "sync", "--quiet")
	var listed []any
	for _, f := range list(e.Manifest("claude", claudeKey)["transcripts"]) {
		if tr := f.(map[string]any); tr["role"] == "tool_result" {
			listed = append(listed, tr["sha256"])
		}
	}
	if diff := cmp.Diff([]any{raw.SHA256([]byte(text))}, listed); diff != "" {
		t.Errorf("tool result sha256 in the manifest (-want +got):\n%s", diff)
	}
}

func TestRewrittenFileKeepsItsEarlierVersion(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	old := readFile(t, path)
	oldSHA := raw.SHA256(old)
	testenv.Write(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "rewritten"}}))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	key := "projects/" + slug + "/" + claudeSID + ".jsonl"
	if !bytes.Equal(readBlob(t, e, "blobs/"+oldSHA+".zst"), old) {
		t.Error("blob doesn't hold the old bytes")
	}
	if !bytes.Equal(readObject(t, e, rawKey("claude", key)), readFile(t, path)) {
		t.Error("object doesn't hold the new bytes")
	}
	versions := list(mainTranscript(t, e.Manifest("claude", claudeKey))["versions"])
	if len(versions) != 1 {
		t.Fatalf("versions = %v", versions)
	}
	v := versions[0].(map[string]any)
	if v["sha256"] != oldSHA || v["size"] != float64(len(old)) || !bytes.Equal(readPieces(t, e, v["pieces"]), old) {
		t.Errorf("version = %v", v)
	}
	index := e.ReadJSON("raw-versions/agent=claude/machine=testbox/" + key + ".json")
	if index["key"] != key || len(list(index["versions"])) != 1 || list(index["versions"])[0].(map[string]any)["sha256"] != oldSHA {
		t.Errorf("index = %v", index)
	}
}

func TestRewriteOfAFileWithSegmentsKeepsEveryPiece(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := rawKey("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "more"}}))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	grown := readFile(t, path)
	testenv.Write(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "rewritten"}}))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	versions := list(mainTranscript(t, e.Manifest("claude", claudeKey))["versions"])
	if len(versions) != 1 {
		t.Fatalf("versions = %v", versions)
	}
	pieces := toPieces(t, versions[0].(map[string]any)["pieces"])
	if len(pieces) != 2 || !strings.HasPrefix(pieces[0].Object, "blobs/") || !strings.HasPrefix(pieces[1].Object, "blobs/") {
		t.Errorf("version pieces = %v", pieces)
	}
	if !bytes.Equal(readPieces(t, e, versions[0].(map[string]any)["pieces"]), grown) {
		t.Error("version doesn't rebuild the grown file")
	}
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("object doesn't hold the rewrite")
	}
	if hasPrefix(e.BucketKeys(), key+".d/") {
		t.Error("segments left after the rewrite")
	}
}

func TestIdenticalOldVersionsAreStoredOnce(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	a := filepath.Join(e.Claude, "plans", "brave-plan.md")
	b := filepath.Join(e.Claude, "plans", "other-plan.md")
	testenv.Write(t, b, string(readFile(t, a)))
	mustRun(t, "sync", "--quiet")
	for _, p := range []string{a, b} {
		testenv.Write(t, p, "changed\n"+filepath.Base(p))
		testenv.Backdate(t, p, 2*time.Minute)
	}
	mustRun(t, "sync", "--quiet")
	var blobs []string
	for _, k := range e.BucketKeys() {
		if strings.HasPrefix(k, "blobs/") {
			blobs = append(blobs, k)
		}
	}
	if len(blobs) != 1 {
		t.Errorf("blobs = %v", blobs)
	}
}

func TestAFileRevertedBackAndForthKeepsEachVersionOnce(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.Claude, "plans", "brave-plan.md")
	a := string(readFile(t, path))
	const b = "# plan b\n"
	mustRun(t, "sync", "--quiet")
	for i, body := range []string{b, a, b} {
		testenv.Write(t, path, body)
		testenv.Backdate(t, path, time.Duration(3-i)*time.Minute)
		mustRun(t, "sync", "--quiet")
	}
	var shas []string
	for _, v := range list(e.ReadJSON("raw-versions/agent=claude/machine=testbox/plans/brave-plan.md.json")["versions"]) {
		shas = append(shas, v.(map[string]any)["sha256"].(string))
	}
	if diff := cmp.Diff([]string{raw.SHA256([]byte(a)), raw.SHA256([]byte(b))}, shas); diff != "" {
		t.Errorf("versions (-want +got):\n%s", diff)
	}
}

func execSQL(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, s := range stmts {
		if _, err := db.ExecContext(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
}

func countRows(t *testing.T, path, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var n int
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDatabaseVersionsAreKeptAtMostDaily(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64<<20, 1<<20, 0)
	dbPath := filepath.Join(e.Home, ".codex", "state_5.sqlite")
	key := rawKey("codex", "db/state_5.sqlite")
	write := func(n string) {
		execSQL(t, dbPath, "CREATE TABLE IF NOT EXISTS t (n INTEGER)", "INSERT INTO t VALUES ("+n+")")
		// Make the hourly copy due.
		_ = filepath.WalkDir(filepath.Join(e.State, "exports", "codex"), func(p string, _ os.DirEntry, err error) error {
			if err == nil && filepath.Base(p) == "state_5.sqlite" {
				testenv.Backdate(t, p, 2*time.Hour)
			}
			return nil
		})
	}
	write("1")
	mustRun(t, "sync", "--quiet")
	for _, n := range []string{"2", "3", "4"} {
		write(n)
		mustRun(t, "sync", "--quiet")
	}
	out := filepath.Join(e.Tmp, "copy.sqlite")
	testenv.Write(t, out, string(readObject(t, e, key)))
	if n := countRows(t, out, "SELECT COUNT(*) FROM t"); n != 4 {
		t.Errorf("copy has %d rows", n)
	}
	if n := countRows(t, filepath.Join(e.State, "state.sqlite3"), "SELECT COUNT(*) FROM raw_versions WHERE key = ?", key); n != 1 {
		t.Errorf("%d versions kept", n)
	}
}

func TestLargeFilesStreamAndKeepVersions(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64, 16, 10*time.Minute)
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	key := rawKey("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
	mustRun(t, "sync", "--quiet")
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Fatal("first upload differs")
	}
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "message", "id": strings.Repeat("x", 50)}))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("appended upload differs")
	}
	if !hasPrefix(e.BucketKeys(), key+".d/") {
		t.Error("no segment for the append")
	}
	if hasPrefix(e.BucketKeys(), "raw-versions/", "blobs/") {
		t.Error("an append kept a version")
	}
	before := readFile(t, path)
	testenv.Write(t, path, strings.Repeat(`{"type":"session","version":3,"id":"replaced"}`+"\n", 3))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	main := mainTranscript(t, e.Manifest("pi", piKey))
	if main["line_count"] != 3.0 {
		t.Errorf("line_count = %v", main["line_count"])
	}
	versions := list(main["versions"])
	if len(versions) != 1 || !bytes.Equal(readPieces(t, e, versions[0].(map[string]any)["pieces"]), before) {
		t.Errorf("versions = %v", versions)
	}
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("rewrite differs")
	}
}

func TestDeletedFileGetsATombstone(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	if err := os.Remove(filepath.Join(e.ClaudeSlug, claudeSID, "subagents", "agent-abc.jsonl")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "sync", "--quiet")
	var gone []string
	for _, d := range list(e.Manifest("claude", claudeKey)["deleted_locally"]) {
		gone = append(gone, d.(map[string]any)["key"].(string))
	}
	want := []string{"projects/" + slug + "/" + claudeSID + "/subagents/agent-abc.jsonl"}
	if diff := cmp.Diff(want, gone); diff != "" {
		t.Fatalf("deleted_locally:\n%s", diff)
	}
	if !e.HasKey(rawKey("claude", gone[0])) {
		t.Error("object removed from the bucket")
	}
}

func TestPartialLastLineIsHeldBackWhileBeingWritten(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	complete := readFile(t, path)
	testenv.Append(t, path, `{"type":"message","id":"half`) // writer mid-line, mtime is now
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
	if hasPrefix(e.BucketKeys(), "raw-versions/", "blobs/") {
		t.Error("an append kept a version")
	}
}

func TestCodexRecompressionKeepsKeyAndSkipsReupload(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	rollout := e.CodexRollout
	key := rawKey("codex", "rollouts/"+filepath.Base(rollout))
	first := mtimes(t, e)[key]
	data := readFile(t, rollout)
	testenv.Write(t, rollout+".zst", string(raw.Compress(data)))
	if err := os.Remove(rollout); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "sync", "--quiet")
	if mtimes(t, e)[key] != first {
		t.Error("recompressed rollout uploaded again")
	}
	if !bytes.Equal(readObject(t, e, key), data) {
		t.Error("object differs")
	}
	if d := list(e.Manifest("codex", codexKey)["deleted_locally"]); len(d) != 0 {
		t.Errorf("deleted_locally = %v", d)
	}
}

// A compressed rollout uploads more bytes than it holds on disk, so whether it changed is
// judged by its size on disk. The file is overwritten here keeping its size and mtime, which a
// sweep that read it again would fail to decompress.
func TestASweepDoesNotReadAnUnchangedCompressedRolloutAgain(t *testing.T) {
	e := testenv.New(t)
	zst := e.CodexRollout + ".zst"
	testenv.Write(t, zst, string(raw.Compress(readFile(t, e.CodexRollout))))
	if err := os.Remove(e.CodexRollout); err != nil {
		t.Fatal(err)
	}
	testenv.Backdate(t, zst, time.Hour)
	mustRun(t, "sync", "--quiet")
	st, err := os.Stat(zst)
	if err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, zst, strings.Repeat("z", int(st.Size())))
	if err := os.Chtimes(zst, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "sync"); code != 0 {
		t.Errorf("sync read the unchanged rollout again: exit %d\n%s", code, out)
	}
}

func TestHookPathOutsideDefaultRootsIsRememberedForSweeps(t *testing.T) {
	e := testenv.New(t)
	custom := filepath.Join(e.Tmp, "pi-custom-sessions")
	sid := "0199a1b2-ffff-7bbb-8ccc-dddddddddddd"
	session := filepath.Join(custom, "2026-10-10T11-00-00-000Z_"+sid+".jsonl")
	testenv.Write(t, session, testenv.JSONL(t, map[string]any{"type": "session", "version": 3, "id": sid, "cwd": e.Project}))
	mustRun(t, "sync", "--quiet", "--agent", "pi", "--path", session)
	var custKeys []string
	for _, k := range e.BucketKeys() {
		if strings.HasPrefix(k, "raw/agent=pi/machine=testbox/dirs/") {
			custKeys = append(custKeys, k)
		}
	}
	if len(custKeys) != 1 || !strings.HasSuffix(custKeys[0], filepath.Base(session)+".zst") {
		t.Fatalf("custom keys = %v", custKeys)
	}
	sid2 := "0199a1b2-eeee-7bbb-8ccc-dddddddddddd"
	testenv.Write(t, filepath.Join(custom, "2026-10-10T12-00-00-000Z_"+sid2+".jsonl"),
		testenv.JSONL(t, map[string]any{"type": "session", "version": 3, "id": sid2}))
	mustRun(t, "sync", "--quiet")
	if !slices.ContainsFunc(e.BucketKeys(), func(k string) bool { return strings.Contains(k, sid2) }) {
		t.Error("sweep missed a session no hook reported")
	}
}

func TestCustomClaudeConfigDirGetsTheSameKeysFromHooksAndSweeps(t *testing.T) {
	e := testenv.New(t)
	other := filepath.Join(e.Tmp, "work-claude")
	sid := "99999999-2222-3333-4444-555555555555"
	session := filepath.Join(other, "projects", "-w", sid+".jsonl")
	testenv.Write(t, session, testenv.JSONL(t, map[string]any{"type": "user", "sessionId": sid}))
	testenv.Write(t, filepath.Join(other, "settings.json"), "{}")
	rootKeys := func() []string {
		var out []string
		for _, k := range e.BucketKeys() {
			if strings.Contains(k, "/roots/") {
				out = append(out, k)
			}
		}
		return out
	}
	t.Setenv("CLAUDE_CONFIG_DIR", other)
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", session)
	hookKeys := rootKeys()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	mustRun(t, "sync", "--quiet", "--agent", "claude")
	sweepKeys := rootKeys()
	if len(hookKeys) == 0 {
		t.Fatal("hook uploaded nothing under roots/")
	}
	for _, k := range hookKeys {
		if !slices.Contains(sweepKeys, k) {
			t.Errorf("sweep used a different key than %s", k)
		}
	}
	if !slices.ContainsFunc(sweepKeys, func(k string) bool { return strings.HasSuffix(k, "/settings.json.zst") }) {
		t.Errorf("sweep missed the custom settings.json: %v", sweepKeys)
	}
	if !e.HasKey(rawKey("claude", "settings.json")) {
		t.Error("~/.claude settings.json key changed")
	}
}

func TestSweepFromACustomConfigDirStillCoversTheDefaultOne(t *testing.T) {
	e := testenv.New(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.Tmp, "work-claude"))
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--trigger", "sweep:SessionStart")
	if !e.HasKey(rawKey("claude", "projects/"+slug+"/"+claudeSID+".jsonl")) {
		t.Error("a sweep started under CLAUDE_CONFIG_DIR skipped ~/.claude")
	}
}

func TestHookSyncPullsInTheWholeClaudeSession(t *testing.T) {
	e := testenv.New(t)
	sub := filepath.Join(e.ClaudeSlug, claudeSID, "subagents", "agent-abc.jsonl")
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", sub, "--trigger", "hook:SubagentStop")
	for _, k := range []string{
		rawKey("claude", "projects/"+slug+"/"+claudeSID+".jsonl"),
		rawKey("claude", "projects/"+slug+"/"+claudeSID+"/tool-results/toolu_1.txt"),
		rawKey("claude", "file-history/"+claudeSID+"/abc@v1"),
		rawKey("claude", "teams/alpha/inboxes/lead.json"),
	} {
		if !e.HasKey(k) {
			t.Errorf("missing %s", k)
		}
	}
	if hasPrefix(e.BucketKeys(), "raw/agent=codex") {
		t.Error("a Claude hook synced Codex files")
	}
	snap := list(e.Manifest("claude", claudeKey)["snapshots"])[0].(map[string]any)
	if snap["timing"] != "in_session" {
		t.Errorf("timing = %v", snap["timing"])
	}
}

func TestMissingOrForeignHookPathIsHarmless(t *testing.T) {
	e := testenv.New(t)
	session := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", filepath.Join(e.Tmp, "nope.txt"), "--path", session)
	if n := lastRunSummary(t, e)["skipped"]; n != 1.0 {
		t.Errorf("skipped = %v, want the foreign path", n)
	}
	notYet := filepath.Join(e.PiDir, "2026-10-10T13-00-00-000Z_0199a1b2-1111-7bbb-8ccc-dddddddddddd.jsonl")
	mustRun(t, "sync", "--quiet", "--agent", "pi", "--path", notYet)
	launch := filepath.Join(e.Tmp, "launch.json")
	testenv.Write(t, launch, `{"event": "SessionStart", "cwd": "`+e.Project+`"}`)
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", filepath.Join(e.Claude, "history.jsonl"), "--launch", launch)
	var manifests []string
	for _, k := range e.BucketKeys() {
		if strings.HasPrefix(k, "manifests/") {
			manifests = append(manifests, k)
		}
	}
	if diff := cmp.Diff([]string{"manifests/agent=claude/machine=testbox/" + claudeKey + ".json"}, manifests); diff != "" {
		t.Errorf("manifests (-want +got):\n%s", diff)
	}
}

func TestHookPathUnderTildeOrRelativeIsResolved(t *testing.T) {
	for _, name := range []string{"tilde", "relative"} {
		t.Run(name, func(t *testing.T) {
			e := testenv.New(t)
			rel, err := filepath.Rel(e.Home, filepath.Join(e.ClaudeSlug, claudeSID+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			path := "~/" + rel
			if name == "relative" {
				t.Chdir(e.Home)
				path = rel
			}
			mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", path)
			if root := e.Manifest("claude", claudeKey)["info"].(map[string]any)["source_root"]; root != e.Claude {
				t.Errorf("source_root = %v", root)
			}
		})
	}
}

func TestLaunchIsRecordedWithAPreSessionSnapshot(t *testing.T) {
	e := testenv.New(t)
	launch := filepath.Join(e.Tmp, "launch.json")
	testenv.Write(t, launch, `{"event": "SessionStart", "source": "startup", "cwd": "`+e.Project+`",
		"permission_mode": "plan", "process": {"argv": ["claude", "--model", "opus"]},
		"env": {"CLAUDE_CODE_ENTRYPOINT": "cli"}}`)
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", path, "--launch", launch, "--trigger", "hook:SessionStart")
	if _, err := os.Stat(launch); !os.IsNotExist(err) {
		t.Error("launch file left behind")
	}
	m := e.Manifest("claude", claudeKey)
	l := m["launch"].(map[string]any)
	if diff := cmp.Diff([]any{"claude", "--model", "opus"}, l["process"].(map[string]any)["argv"]); diff != "" {
		t.Errorf("argv:\n%s", diff)
	}
	if l["permission_mode"] != "plan" {
		t.Errorf("permission_mode = %v", l["permission_mode"])
	}
	git := m["git"].(map[string]any)
	if l["git"].(map[string]any)["head"] != git["head"] || git["dirty"] != false {
		t.Errorf("launch git %v, manifest git %v", l["git"], git)
	}
	snaps := list(m["snapshots"])
	if len(snaps) != 1 || snaps[0].(map[string]any)["timing"] != "pre_session" || l["snapshot"] != snaps[0].(map[string]any)["id"] {
		t.Errorf("snapshots = %v, launch snapshot %v", snaps, l["snapshot"])
	}
}

func TestLaunchFallsBackToTheHookInputCwd(t *testing.T) {
	e := testenv.New(t)
	launch := filepath.Join(e.Tmp, "launch.json")
	testenv.Write(t, launch, `{"event": "SessionStart", "cwd": null, "hook_input": {"cwd": "`+e.Project+`"}}`)
	mustRun(t, "sync", "--quiet", "--agent", "codex", "--path", e.CodexRollout, "--launch", launch)
	m := e.Manifest("codex", codexKey)
	snaps := list(m["snapshots"])
	if len(snaps) != 1 || snaps[0].(map[string]any)["timing"] != "pre_session" {
		t.Errorf("snapshots = %v", snaps)
	}
	if m["launch"].(map[string]any)["git"].(map[string]any)["head"] == nil {
		t.Errorf("launch git = %v", m["launch"])
	}
}

// TestALaunchBeforeTheTranscriptExistsGivesTheSessionItsCwd records the start of a session
// Claude Code hasn't written a line of yet, so only the launch knows its working directory.
func TestALaunchBeforeTheTranscriptExistsGivesTheSessionItsCwd(t *testing.T) {
	e := testenv.New(t)
	sid := "22222222-2222-3333-4444-555555555555"
	launch := filepath.Join(e.Tmp, "launch.json")
	testenv.Write(t, launch, `{"event": "SessionStart", "cwd": "`+e.Project+`"}`)
	path := filepath.Join(e.ClaudeSlug, sid+".jsonl")
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", path, "--launch", launch, "--trigger", "hook:SessionStart")
	if info := e.Manifest("claude", slug+"/"+sid)["info"].(map[string]any); info["cwd"] != e.Project {
		t.Errorf("info = %v", info)
	}
}

// TestASessionWithNoKnownCwdGetsNoSnapshot syncs a transcript whose lines don't say where the
// session ran: a snapshot would leave out the project's files without saying so.
func TestASessionWithNoKnownCwdGetsNoSnapshot(t *testing.T) {
	e := testenv.New(t)
	sid := "33333333-2222-3333-4444-555555555555"
	session := filepath.Join(e.ClaudeSlug, sid+".jsonl")
	testenv.Write(t, session, testenv.JSONL(t, map[string]any{"type": "user", "sessionId": sid}))
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", session)
	if snaps := list(e.Manifest("claude", slug+"/"+sid)["snapshots"]); len(snaps) != 0 {
		t.Errorf("snapshots = %v", snaps)
	}
}

func TestBackfillSnapshotsArePostHoc(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "backfill")
	snap := list(e.Manifest("pi", piKey)["snapshots"])[0].(map[string]any)
	if snap["timing"] != "post_hoc" {
		t.Errorf("timing = %v", snap["timing"])
	}
}

// TestASweepDuringASessionTakesAnInSessionSnapshot sweeps a session whose main transcript was
// written two minutes ago and its other files an hour ago.
func TestASweepDuringASessionTakesAnInSessionSnapshot(t *testing.T) {
	e := testenv.New(t)
	testenv.Backdate(t, filepath.Join(e.ClaudeSlug, claudeSID+".jsonl"), 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	snap := list(e.Manifest("claude", claudeKey)["snapshots"])[0].(map[string]any)
	if snap["timing"] != "in_session" {
		t.Errorf("timing = %v", snap["timing"])
	}
}

func TestCodexDatabasesAreCopiedWithTheBackupAPI(t *testing.T) {
	e := testenv.New(t)
	dbPath := filepath.Join(e.Home, ".codex", "state_5.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, tokens_used INTEGER)",
		"INSERT INTO threads VALUES ('" + codexThread + "', 'Fix the build', 1234)",
	} {
		if _, err := db.ExecContext(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	// Left open: the row is still only in the -wal file.
	mustRun(t, "sync", "--quiet")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, k := range e.BucketKeys() {
		if strings.HasSuffix(k, "-wal.zst") || strings.HasSuffix(k, "-shm.zst") {
			t.Errorf("copied %s", k)
		}
	}
	out := filepath.Join(e.Tmp, "copy.sqlite")
	testenv.Write(t, out, string(readObject(t, e, rawKey("codex", "db/state_5.sqlite"))))
	if n := countRows(t, out, "SELECT COUNT(*) FROM threads WHERE id = ? AND title = 'Fix the build' AND tokens_used = 1234", codexThread); n != 1 {
		t.Error("the copy lost the row in the WAL")
	}
}

func TestEverythingTheCollectorWritesIsPrivate(t *testing.T) {
	e := testenv.New(t)
	db, err := sql.Open("sqlite", filepath.Join(e.Home, ".codex", "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE threads (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "sync", "--quiet")
	for _, root := range []string{e.State, e.Bucket} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			st, err := d.Info()
			if err != nil {
				return err
			}
			if st.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s is %v", path, st.Mode().Perm())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotRecordsWholeHarnessWithSecretsRedacted(t *testing.T) {
	e := testenv.New(t)
	target := filepath.Join(e.Tmp, "dotfiles", "review")
	testenv.Write(t, filepath.Join(target, "SKILL.md"), "review skill\n")
	if err := os.Symlink(target, filepath.Join(e.Claude, "skills", "review")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "sync", "--quiet", "--agent", "claude")
	snap := list(e.Manifest("claude", claudeKey)["snapshots"])[0].(map[string]any)
	doc := e.ReadJSON("snapshots/sets/" + snap["id"].(string) + ".json")
	if doc["kind"] != "harness" {
		t.Errorf("kind = %v", doc["kind"])
	}
	files := map[[2]string]map[string]any{}
	for _, f := range list(doc["files"]) {
		m := f.(map[string]any)
		files[[2]string{m["scope"].(string), m["path"].(string)}] = m
	}
	resolved := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, k := range [][2]string{
		{"user", "CLAUDE.md"},
		{"user", "settings.json"},
		{"user", "skills/deploy/SKILL.md"},
		{"user", "skills/deploy/scripts/go.sh"},
		{"user", "skills/review/SKILL.md"},
		{"user", "hooks/guard.sh"},
		{"project", "CLAUDE.md"},
		{"project", "src/CLAUDE.md"},
		{"project", ".claude/settings.json"},
		{"ancestor", filepath.Join(e.Home, "code", "CLAUDE.md")},
		{"user", ".claude.json#mcp"},
		{"script", resolved(filepath.Join(e.Claude, "hooks", "guard.sh"))},
		{"script", resolved(filepath.Join(e.Home, "bin", "status.py"))},
		{"import", resolved(filepath.Join(e.Project, "docs", "conventions.md"))},
	} {
		if files[k] == nil {
			t.Errorf("snapshot lacks %v", k)
		}
	}
	if got := files[[2]string{"user", "skills/review/SKILL.md"}]["target"]; got != resolved(filepath.Join(target, "SKILL.md")) {
		t.Errorf("symlink target = %v", got)
	}
	if _, ok := snap["mtimes"].(map[string]any)["harness:user:CLAUDE.md"]; !ok {
		t.Error("no mtime for CLAUDE.md")
	}
	blob := func(scope, path string) string {
		return string(readFile(t, filepath.Join(e.Bucket, "snapshots", "blobs", files[[2]string{scope, path}]["sha256"].(string))))
	}
	for _, c := range []struct{ scope, path, kept, gone string }{
		{"user", "settings.json", "guard.sh", "sk-ant-"},
		{"project", ".claude/settings.json", "DB_PASSWORD", "hunter2"},
		{"user", ".claude.json#mcp", "GITHUB_TOKEN", "ghp_"},
	} {
		body := blob(c.scope, c.path)
		if !strings.Contains(body, c.kept) || strings.Contains(body, c.gone) || !strings.Contains(body, "[redacted]") {
			t.Errorf("%s %s = %s", c.scope, c.path, body)
		}
	}
	memory := e.ReadJSON("snapshots/sets/" + snap["memory_id"].(string) + ".json")
	var scopes []string
	for _, f := range list(memory["files"]) {
		scopes = append(scopes, f.(map[string]any)["scope"].(string))
	}
	if memory["kind"] != "memory" || !slices.Equal(scopes, []string{"auto"}) {
		t.Errorf("memory set = %v", memory)
	}
}
