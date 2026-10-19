package syncer_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/mich-murphy/memory/internal/cli"
	launchpkg "github.com/mich-murphy/memory/internal/launch"
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

var fileStream = testenv.FileStream

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

// readObject rebuilds the current generation from the unique immutable commit chain,
// without consulting a manifest or the disposable local state.
func readObject(t *testing.T, e *testenv.Env, prefix string) []byte {
	t.Helper()
	records := e.Records(prefix)
	if len(records) == 0 {
		t.Fatalf("no file commits in %s", prefix)
	}
	// Every historical generation remains independently reconstructible.
	for i := range records {
		e.GenerationContent(records[i].File)
	}
	return e.GenerationContent(records[len(records)-1].File)
}

func readBlob(t *testing.T, e *testenv.Env, key string) []byte {
	t.Helper()
	data, err := raw.Decompress(readFile(t, filepath.Join(e.Bucket, key)))
	if err != nil {
		t.Fatal(err)
	}
	return data
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

// generations resolves a manifest transcript to its stream's history, oldest first.
func generations(t *testing.T, e *testenv.Env, tr map[string]any) []*raw.Generation {
	t.Helper()
	var out []*raw.Generation
	records := e.Records(strings.TrimSuffix(tr["history"].(string), "commits/"))
	for i := range records {
		out = append(out, records[i].File)
	}
	if len(out) == 0 {
		t.Fatalf("no history for %v", tr["key"])
	}
	return out
}

func currentGeneration(t *testing.T, e *testenv.Env, tr map[string]any) *raw.Generation {
	t.Helper()
	history := generations(t, e, tr)
	return history[len(history)-1]
}

func claudeManifests(e *testenv.Env) int {
	return len(e.Records(testenv.Stream(testenv.MachineID, "session", "claude", claudeKey)))
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func mtimes(t *testing.T, e *testenv.Env) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, k := range e.BucketKeys() {
		// A coordination checkpoint is mutable; immutable archive evidence is not.
		if strings.HasSuffix(k, "/checkpoint.json") {
			continue
		}
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
		fileStream("claude", p+"/"+claudeSID+".jsonl"),
		fileStream("claude", p+"/"+claudeSID+"/subagents/agent-abc.jsonl"),
		fileStream("claude", p+"/"+claudeSID+"/subagents/agent-abc.meta.json"),
		fileStream("claude", p+"/"+claudeSID+"/tool-results/toolu_1.txt"),
		fileStream("claude", p+"/"+claudeSID+".jsonl.superseded-20261001"),
		fileStream("claude", p+"/memory/MEMORY.md"),
		fileStream("claude", "plans/brave-plan.md"),
		fileStream("claude", "history.jsonl"),
		fileStream("claude", "settings.json"),
		fileStream("claude", "skills/deploy/scripts/go.sh"),
		fileStream("claude", "file-history/"+claudeSID+"/abc@v1"),
		fileStream("claude", "teams/alpha/inboxes/lead.json"),
		fileStream("claude", "sessions/123.json"),
		fileStream("claude", "mystery.dat"),
		fileStream("claude", "claude.json"),
		fileStream("codex", "rollouts/"+codexKey+".jsonl"),
		fileStream("codex", "history.jsonl"),
		fileStream("codex", "config.toml"),
		fileStream("codex", "memories/notes.md"),
		fileStream("pi", "sessions/--home-code-proj--/"+piKey+".jsonl"),
		fileStream("pi", "AGENTS.md"),
		testenv.Stream(testenv.MachineID, "session", "claude", claudeKey),
		testenv.Stream(testenv.MachineID, "session", "codex", codexKey),
		testenv.Stream(testenv.MachineID, "session", "pi", testenv.PiSessionKey),
	} {
		if !e.HasStream(k) {
			t.Errorf("missing %s", k)
		}
	}
	if err := testenv.Audit(e.BucketKeys(), e.ReadObject); err != nil {
		t.Fatal(err)
	}
	original := readFile(t, filepath.Join(e.ClaudeSlug, claudeSID+".jsonl"))
	if got := readObject(t, e, fileStream("claude", p+"/"+claudeSID+".jsonl")); !bytes.Equal(got, original) {
		t.Errorf("stored transcript differs:\n%s", cmp.Diff(string(original), string(got)))
	}

	m := e.Manifest("claude", claudeKey)
	info := m["info"].(map[string]any)
	if m["schema"] != 5.0 || m["machine_id"] != "testbox" || m["hostname"] == "" {
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
	if g := currentGeneration(t, e, main); g.LineCount != 2 || g.PrefixSHA256 == "" || g.Head["session_id"] != claudeSID {
		t.Errorf("main transcript = %v", g)
	}
	if len(main) != 3 {
		t.Errorf("main transcript copies its generation: %v", main)
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
	pi := e.Manifest("pi", testenv.PiSessionKey)["info"].(map[string]any)
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
	// Physical record keys are hashes: exclusions must be checked against decoded
	// logical identities, not obsolete mutable-object suffixes.
	for _, agent := range []string{"claude", "codex", "pi"} {
		for _, r := range e.FileRecords(agent) {
			for _, bad := range []string{".credentials.json", "auth.json", ".key"} {
				if strings.HasSuffix(r.Key, bad) {
					t.Errorf("copied excluded file %s/%s", agent, r.Key)
				}
			}
			if strings.Contains(r.Key, "shell-snapshots") {
				t.Errorf("copied noise %s/%s", agent, r.Key)
			}
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
	if err := json.Unmarshal(readObject(t, e, fileStream("claude", "claude.json")), &claudeJSON); err != nil {
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
	if e.HasStream(fileStream("claude", "agent-sessions.env")) {
		t.Error("copied the collector's config")
	}
}

func TestAppendedSessionUploadsOnlyNewBytesThenFolds(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := fileStream("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	first := int64(len(readFile(t, path)))
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "more"}}))
	testenv.Backdate(t, path, 2*time.Minute) // written two minutes ago: the session is still going
	mustRun(t, "sync", "--quiet")
	now := readFile(t, path)
	history := e.Records(key)
	if len(history) != 2 || len(history[1].File.Pieces) != 2 {
		t.Fatalf("append history = %v", history)
	}
	segment := history[1].File.Pieces[1]
	if segment.Start != first || segment.End != int64(len(now)) || !bytes.Equal(readBlob(t, e, segment.Object), now[first:]) {
		t.Errorf("append piece = %v", segment)
	}
	if !bytes.Equal(readObject(t, e, key), now) {
		t.Error("rebuilt object differs")
	}
	if n := claudeManifests(e); n != 1 {
		t.Errorf("an append published %d manifests, want the first one only", n)
	}
	main := mainTranscript(t, e.Manifest("claude", claudeKey))
	if g := currentGeneration(t, e, main); g.Size != int64(len(now)) || g.LineCount != 3 || len(g.Pieces) != 2 || g.Pieces[1].Start != first {
		t.Errorf("main = %v", g)
	}
	appendArchivePieces(t, path, 62)
	now = readFile(t, path)
	before := mtimes(t, e)
	mustRun(t, "sync", "--quiet")
	history = e.Records(key)
	if len(history) != 65 || history[len(history)-1].File.Kind != "fold" || len(history[len(history)-1].File.Pieces) != 2 {
		t.Fatalf("fold history = %v", history)
	}
	if !bytes.Equal(e.GenerationContent(history[len(history)-1].File), now) {
		t.Error("folded content differs")
	}
	after := mtimes(t, e)
	for object, mtime := range before {
		if after[object] != mtime {
			t.Errorf("fold removed or rewrote %s", object)
		}
	}
	if n := claudeManifests(e); n != 1 {
		t.Errorf("a fold published %d manifests, want the first one only", n)
	}
	before = mtimes(t, e)
	mustRun(t, "sync", "--quiet")
	if diff := cmp.Diff(before, mtimes(t, e)); diff != "" {
		t.Errorf("idle fold changed objects:\n%s", diff)
	}
}

// TestAChangeToASessionsOtherFilesReachesItsManifest changes only a tool result, which has
// no head to read: the manifest's reference resolves to the new content without a new manifest.
func TestAChangeToASessionsOtherFilesReachesItsManifest(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	const text = "bigger output\n"
	testenv.Write(t, filepath.Join(e.ClaudeSlug, claudeSID, "tool-results", "toolu_1.txt"), text)
	mustRun(t, "sync", "--quiet")
	var listed []any
	for _, f := range list(e.Manifest("claude", claudeKey)["transcripts"]) {
		if tr := f.(map[string]any); tr["role"] == "tool_result" {
			listed = append(listed, currentGeneration(t, e, tr).SHA256)
		}
	}
	if diff := cmp.Diff([]any{raw.SHA256([]byte(text))}, listed); diff != "" {
		t.Errorf("tool result sha256 through the manifest (-want +got):\n%s", diff)
	}
	if n := claudeManifests(e); n != 1 {
		t.Errorf("a rewrite published %d manifests, want the first one only", n)
	}
}

// TestANewFileInASessionRefreshesItsManifest adds a subagent transcript, which the manifest
// has to list.
func TestANewFileInASessionRefreshesItsManifest(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	key := "projects/" + slug + "/" + claudeSID + "/subagents/agent-new.jsonl"
	testenv.Write(t, filepath.Join(e.ClaudeSlug, claudeSID, "subagents", "agent-new.jsonl"), testenv.JSONL(t, map[string]any{"type": "user", "sessionId": claudeSID, "agentId": "new"}))
	mustRun(t, "sync", "--quiet")
	found := false
	for _, f := range list(e.Manifest("claude", claudeKey)["transcripts"]) {
		found = found || f.(map[string]any)["key"] == key
	}
	if !found || claudeManifests(e) != 2 {
		t.Errorf("new subagent listed %v, manifests %d, want listed in a second manifest", found, claudeManifests(e))
	}
}

// A state from before file anchors were recorded finds them in each file's history.
func TestAStateWithoutFileAnchorsDependsOnTheSameCommits(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	stream := testenv.Stream(testenv.MachineID, "session", "claude", claudeKey)
	fileDeps := func(r testenv.Record) []string {
		var keys []string
		for _, d := range r.Dependencies {
			if strings.Contains(d.Object, "/records/file/") {
				keys = append(keys, d.Object)
			}
		}
		return keys
	}
	before := fileDeps(e.Records(stream)[0])
	// An append moves the main transcript's tip but not its anchor.
	main := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	testenv.Append(t, main, testenv.JSONL(t, map[string]any{"type": "user", "sessionId": claudeSID}))
	testenv.Backdate(t, main, time.Hour)
	mustRun(t, "sync", "--quiet")
	execSQL(t, filepath.Join(e.State, "state.sqlite3"), "DELETE FROM file_anchors")
	testenv.Write(t, filepath.Join(e.ClaudeSlug, claudeSID, "subagents", "agent-new.jsonl"), testenv.JSONL(t, map[string]any{"type": "user", "sessionId": claudeSID, "agentId": "new"}))
	mustRun(t, "sync", "--quiet")
	records := e.Records(stream)
	after := fileDeps(records[len(records)-1])
	if len(records) != 2 || len(after) != len(before)+1 {
		t.Fatalf("manifests %d, file dependencies %d -> %d, want one more in a second manifest", len(records), len(before), len(after))
	}
	for _, key := range before {
		if !slices.Contains(after, key) {
			t.Errorf("rebuilt anchors dropped %s", key)
		}
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
	history := e.History("claude", key)
	if len(history) != 2 || !bytes.Equal(e.GenerationContent(history[0].File), old) {
		t.Error("blob doesn't hold the old bytes")
	}
	if !bytes.Equal(readObject(t, e, fileStream("claude", key)), readFile(t, path)) {
		t.Error("object doesn't hold the new bytes")
	}
	versions := generations(t, e, mainTranscript(t, e.Manifest("claude", claudeKey)))
	if len(versions) != 2 {
		t.Fatalf("versions = %v", versions)
	}
	if v := versions[0]; v.SHA256 != oldSHA || v.Size != int64(len(old)) || !bytes.Equal(e.GenerationContent(v), old) {
		t.Errorf("version = %v", v)
	}
	if history[1].Parent != history[0].ID || history[1].File.SHA256 != raw.SHA256(readFile(t, path)) {
		t.Errorf("rewrite chain = %v", history)
	}
}

func TestRewriteOfAFileWithSegmentsKeepsEveryPiece(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	key := fileStream("claude", "projects/"+slug+"/"+claudeSID+".jsonl")
	testenv.Append(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "more"}}))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	grown := readFile(t, path)
	testenv.Write(t, path, testenv.JSONL(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "rewritten"}}))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	versions := generations(t, e, mainTranscript(t, e.Manifest("claude", claudeKey)))
	if len(versions) != 3 {
		t.Fatalf("versions = %v", versions)
	}
	pieces := versions[1].Pieces
	if len(pieces) != 2 || !strings.HasPrefix(pieces[0].Object, "v3/blobs/zstd/") || !strings.HasPrefix(pieces[1].Object, "v3/blobs/zstd/") {
		t.Errorf("version pieces = %v", pieces)
	}
	if !bytes.Equal(e.GenerationContent(versions[1]), grown) {
		t.Error("version doesn't rebuild the grown file")
	}
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("object doesn't hold the rewrite")
	}
	history := e.Records(key)
	if len(history) != 3 || len(history[1].File.Pieces) != 2 || len(history[2].File.Pieces) != 1 {
		t.Errorf("rewrite history = %v", history)
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
	ha, hb := e.History("claude", "plans/brave-plan.md"), e.History("claude", "plans/other-plan.md")
	if len(ha) != 2 || len(hb) != 2 {
		t.Fatalf("histories = %v %v", ha, hb)
	}
	if diff := cmp.Diff(ha[0].File.Pieces, hb[0].File.Pieces); diff != "" {
		t.Errorf("identical bytes not deduplicated:\n%s", diff)
	}
	object := ha[0].File.Pieces[0].Object
	if !e.HasKey(object) {
		t.Errorf("missing shared blob %s", object)
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
	history := e.History("claude", "plans/brave-plan.md")
	var shas []string
	for _, r := range history {
		shas = append(shas, r.File.SHA256)
		e.GenerationContent(r.File)
	}
	want := []string{raw.SHA256([]byte(a)), raw.SHA256([]byte(b)), raw.SHA256([]byte(a)), raw.SHA256([]byte(b))}
	if diff := cmp.Diff(want, shas); diff != "" {
		t.Errorf("history (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(history[0].File.Pieces, history[2].File.Pieces); diff != "" {
		t.Errorf("reverted A duplicated blobs:\n%s", diff)
	}
	if diff := cmp.Diff(history[1].File.Pieces, history[3].File.Pieces); diff != "" {
		t.Errorf("reverted B duplicated blobs:\n%s", diff)
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

func TestDatabaseRefreshesRetainEveryImmutableGeneration(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64<<20, 1<<20, 0)
	dbPath := filepath.Join(e.Home, ".codex", "state_5.sqlite")
	key := fileStream("codex", "db/state_5.sqlite")
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
	history := e.Records(key)
	if len(history) != 4 {
		t.Fatalf("database generations = %d, want4", len(history))
	}
	for i := range history {
		testenv.Write(t, out, string(e.GenerationContent(history[i].File)))
		if n := countRows(t, out, "SELECT COUNT(*) FROM t"); n != i+1 {
			t.Errorf("generation %d database rows = %d, want %d", i, n, i+1)
		}
	}
}

func TestLargeFilesStreamAndKeepVersions(t *testing.T) {
	e := testenv.New(t)
	syncer.SetLimits(t, 64, 16, 10*time.Minute)
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	key := fileStream("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
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
	if history := e.Records(key); len(history) != 2 || len(history[1].File.Pieces) != 2 {
		t.Error("no segment for the append")
	}
	if history := e.Records(key); len(history) != 2 {
		t.Errorf("append history = %v", history)
	}
	before := readFile(t, path)
	testenv.Write(t, path, strings.Repeat(`{"type":"session","version":3,"id":"replaced"}`+"\n", 3))
	testenv.Backdate(t, path, 2*time.Minute)
	mustRun(t, "sync", "--quiet")
	versions := generations(t, e, mainTranscript(t, e.Manifest("pi", testenv.PiSessionKey)))
	if n := len(versions); n != 3 || versions[n-1].LineCount != 3 || !bytes.Equal(e.GenerationContent(versions[n-2]), before) {
		t.Errorf("versions = %v", versions)
	}
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("rewrite differs")
	}
}

func TestDeletedFileGetsATombstone(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	old := e.History("claude", "projects/"+slug+"/"+claudeSID+"/subagents/agent-abc.jsonl")
	content := e.GenerationContent(old[0].File)
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
	history := e.History("claude", gone[0])
	if len(history) != 2 || history[1].File.Kind != "delete" || history[1].File.DeletedAt <= 0 || !bytes.Equal(e.GenerationContent(history[1].File), content) {
		t.Errorf("deletion history = %v", history)
	}
	if diff := cmp.Diff(old[0].File.Pieces, history[1].File.Pieces); diff != "" {
		t.Errorf("deletion changed content pieces:\n%s", diff)
	}
}

func TestPartialLastLineIsHeldBackWhileBeingWritten(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	path := filepath.Join(e.PiDir, piKey+".jsonl")
	complete := readFile(t, path)
	testenv.Append(t, path, `{"type":"message","id":"half`) // writer mid-line, mtime is now
	mustRun(t, "sync", "--quiet")
	key := fileStream("pi", "sessions/--home-code-proj--/"+filepath.Base(path))
	if !bytes.Equal(readObject(t, e, key), complete) {
		t.Error("half a line was uploaded")
	}
	testenv.Append(t, path, "\"}\n")
	mustRun(t, "sync", "--quiet")
	if !bytes.Equal(readObject(t, e, key), readFile(t, path)) {
		t.Error("completed line missing")
	}
	if history := e.Records(key); len(history) != 2 {
		t.Errorf("append history = %v", history)
	}
}

func TestCodexRecompressionKeepsKeyAndSkipsReupload(t *testing.T) {
	e := testenv.New(t)
	mustRun(t, "sync", "--quiet")
	rollout := e.CodexRollout
	key := fileStream("codex", "rollouts/"+filepath.Base(rollout))
	first := e.Records(key)[0].ID
	data := readFile(t, rollout)
	testenv.Write(t, rollout+".zst", string(raw.Compress(data)))
	if err := os.Remove(rollout); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "sync", "--quiet")
	if records := e.Records(key); len(records) != 1 || records[0].ID != first {
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
	for _, r := range e.FileRecords("pi") {
		if strings.HasPrefix(r.Key, "dirs/") {
			custKeys = append(custKeys, r.Key)
		}
	}
	if len(custKeys) != 1 || !strings.HasSuffix(custKeys[0], filepath.Base(session)) {
		t.Fatalf("custom keys = %v", custKeys)
	}
	sid2 := "0199a1b2-eeee-7bbb-8ccc-dddddddddddd"
	testenv.Write(t, filepath.Join(custom, "2026-10-10T12-00-00-000Z_"+sid2+".jsonl"),
		testenv.JSONL(t, map[string]any{"type": "session", "version": 3, "id": sid2}))
	mustRun(t, "sync", "--quiet")
	if !slices.ContainsFunc(e.FileRecords("pi"), func(r testenv.Record) bool { return strings.Contains(r.Key, sid2) }) {
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
		for _, r := range e.FileRecords("claude") {
			if strings.HasPrefix(r.Key, "roots/") {
				out = append(out, r.Key)
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
	if !slices.ContainsFunc(sweepKeys, func(k string) bool { return strings.HasSuffix(k, "/settings.json") }) {
		t.Errorf("sweep missed the custom settings.json: %v", sweepKeys)
	}
	if !e.HasStream(fileStream("claude", "settings.json")) {
		t.Error("~/.claude settings.json key changed")
	}
}

func TestSweepFromACustomConfigDirStillCoversTheDefaultOne(t *testing.T) {
	e := testenv.New(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.Tmp, "work-claude"))
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--trigger", "sweep:SessionStart")
	if !e.HasStream(fileStream("claude", "projects/"+slug+"/"+claudeSID+".jsonl")) {
		t.Error("a sweep started under CLAUDE_CONFIG_DIR skipped ~/.claude")
	}
}

func TestHookSyncPullsInTheWholeClaudeSession(t *testing.T) {
	e := testenv.New(t)
	sub := filepath.Join(e.ClaudeSlug, claudeSID, "subagents", "agent-abc.jsonl")
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", sub, "--trigger", "hook:SubagentStop")
	for _, k := range []string{
		fileStream("claude", "projects/"+slug+"/"+claudeSID+".jsonl"),
		fileStream("claude", "projects/"+slug+"/"+claudeSID+"/tool-results/toolu_1.txt"),
		fileStream("claude", "file-history/"+claudeSID+"/abc@v1"),
		fileStream("claude", "teams/alpha/inboxes/lead.json"),
	} {
		if !e.HasStream(k) {
			t.Errorf("missing %s", k)
		}
	}
	if len(e.FileRecords("codex")) > 0 {
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
	history := filepath.Join(e.Claude, "history.jsonl")
	launch := leaveLaunch(t, e, "claude", history, `{"event": "SessionStart", "cwd": "`+e.Project+`"}`)
	originalQueue := readFile(t, launch)
	if code, _ := run(t, "sync", "--quiet", "--agent", "claude", "--path", history, "--launch", launch); code != 1 {
		t.Errorf("foreign launch exit = %d, want 1", code)
	}
	if !bytes.Equal(readFile(t, launch), originalQueue) {
		t.Fatal("foreign launch queue bytes changed")
	}
	var manifests []string
	for _, k := range e.BucketKeys() {
		if strings.Contains(k, "/records/session/") && strings.Contains(k, "/commits/") {
			manifests = append(manifests, k)
		}
	}
	expected := e.Records(testenv.Stream(testenv.MachineID, "session", "claude", claudeKey))
	if len(manifests) != len(expected) || len(expected) == 0 {
		t.Errorf("unexpected manifest streams: %v", manifests)
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

// leaveLaunch leaves a launch where a session-start hook does, for the sync it starts.
func leaveLaunch(t *testing.T, e *testenv.Env, agent, path, launch string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(launch), &m); err != nil {
		t.Fatal(err)
	}
	m["agent"], m["path"] = agent, path
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := launchpkg.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.State, "pending", "launch-test.json")
	testenv.Write(t, file, string(b))
	return file
}

func TestLaunchIsRecordedWithAPreSessionSnapshot(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.ClaudeSlug, claudeSID+".jsonl")
	launch := leaveLaunch(t, e, "claude", path, `{"event": "SessionStart", "source": "startup", "cwd": "`+e.Project+`",
		"permission_mode": "plan", "process": {"argv": ["claude", "--model", "opus"]},
		"env": {"CLAUDE_CODE_ENTRYPOINT": "cli"}}`)
	mustRun(t, "sync", "--quiet", "--agent", "claude", "--path", path, "--launch", launch, "--trigger", "hook:SessionStart")
	if _, err := os.Stat(launch); !os.IsNotExist(err) {
		t.Error("launch file left behind")
	}
	m := e.Manifest("claude", claudeKey)
	launches := e.Launches(m)
	if len(launches) != 1 || m["launch"] != nil || m["launches"] != nil {
		t.Fatalf("manifest launches = %v, launch_records %v, want one record and no copy", launches, m["launch_records"])
	}
	l := launches[0]
	envelope := l["envelope"].(map[string]any)
	payload := envelope["payload"].(map[string]any)
	if envelope["schema"] != 1.0 || envelope["agent"] != "claude" || envelope["path"] != path || envelope["id"] == "" {
		t.Errorf("launch envelope = %v", envelope)
	}
	if diff := cmp.Diff([]any{"claude", "--model", "opus"}, payload["process"].(map[string]any)["argv"]); diff != "" {
		t.Errorf("argv:\n%s", diff)
	}
	if payload["permission_mode"] != "plan" {
		t.Errorf("permission_mode = %v", payload["permission_mode"])
	}
	git := m["git"].(map[string]any)
	if l["git"].(map[string]any)["head"] != git["head"] || git["dirty"] != false {
		t.Errorf("launch git %v, manifest git %v", l["git"], git)
	}
	snaps := list(m["snapshots"])
	if len(snaps) != 1 || snaps[0].(map[string]any)["timing"] != "captured" || l["snapshot"].(map[string]any)["id"] != snaps[0].(map[string]any)["id"] {
		t.Errorf("snapshots = %v, launch snapshot %v", snaps, l["snapshot"])
	}
}

func TestLaunchFallsBackToTheHookInputCwd(t *testing.T) {
	e := testenv.New(t)
	launch := leaveLaunch(t, e, "codex", e.CodexRollout, `{"event": "SessionStart", "cwd": null, "hook_input": {"cwd": "`+e.Project+`"}}`)
	mustRun(t, "sync", "--quiet", "--agent", "codex", "--path", e.CodexRollout, "--launch", launch)
	m := e.Manifest("codex", codexKey)
	snaps := list(m["snapshots"])
	if len(snaps) != 1 || snaps[0].(map[string]any)["timing"] != "captured" {
		t.Errorf("snapshots = %v", snaps)
	}
	if l := e.Launches(m); len(l) != 1 || l[0]["git"].(map[string]any)["head"] == nil {
		t.Errorf("launch = %v", l)
	}
}

// TestALaunchBeforeTheTranscriptExistsGivesTheSessionItsCwd records the start of a session
// Claude Code hasn't written a line of yet, so only the launch knows its working directory.
func TestALaunchBeforeTheTranscriptExistsGivesTheSessionItsCwd(t *testing.T) {
	e := testenv.New(t)
	sid := "22222222-2222-3333-4444-555555555555"
	path := filepath.Join(e.ClaudeSlug, sid+".jsonl")
	launch := leaveLaunch(t, e, "claude", path, `{"event": "SessionStart", "cwd": "`+e.Project+`"}`)
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
	snap := list(e.Manifest("pi", testenv.PiSessionKey)["snapshots"])[0].(map[string]any)
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
	for _, r := range e.FileRecords("codex") {
		if strings.HasSuffix(r.Key, "-wal") || strings.HasSuffix(r.Key, "-shm") {
			t.Errorf("copied live SQLite sidecar %s", r.Key)
		}
	}
	out := filepath.Join(e.Tmp, "copy.sqlite")
	testenv.Write(t, out, string(readObject(t, e, fileStream("codex", "db/state_5.sqlite"))))
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
	doc := e.ReadJSON("v3/snapshots/sets/" + snap["id"].(string) + ".json")
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
	if _, ok := e.Mtimes(snap)["harness:user:CLAUDE.md"]; !ok || snap["mtimes"] != nil {
		t.Error("no mtime for CLAUDE.md")
	}
	blob := func(scope, path string) string {
		return string(readFile(t, filepath.Join(e.Bucket, "v3", "blobs", "plain", files[[2]string{scope, path}]["sha256"].(string))))
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
	memory := e.ReadJSON("v3/snapshots/sets/" + snap["memory_id"].(string) + ".json")
	var scopes []string
	for _, f := range list(memory["files"]) {
		scopes = append(scopes, f.(map[string]any)["scope"].(string))
	}
	if memory["kind"] != "memory" || !slices.Equal(scopes, []string{"auto"}) {
		t.Errorf("memory set = %v", memory)
	}
}

// A truncation to zero is a committed content observation, not "nothing to assert".
// Growing it again must use a positive-width piece while keeping the empty generation.
func TestEmptyGenerationCanGrowWithoutLosingHistory(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.Claude, "plans", "brave-plan.md")
	bodies := []string{"a\n", "", "b\n"}
	for _, body := range bodies {
		testenv.Write(t, path, body)
		testenv.Backdate(t, path, 2*time.Minute)
		mustRun(t, "sync", "--quiet")
	}
	history := e.History("claude", "plans/brave-plan.md")
	if len(history) != len(bodies) {
		t.Fatalf("history length = %d, want %d", len(history), len(bodies))
	}
	for i := range history {
		if got := e.GenerationContent(history[i].File); !bytes.Equal(got, []byte(bodies[i])) {
			t.Errorf("generation %d = %q, want %q", i, got, bodies[i])
		}
	}
	if len(history[2].File.Pieces) != 1 || history[2].File.Pieces[0].Start != 0 || history[2].File.Pieces[0].End != 2 {
		t.Errorf("grown pieces = %v", history[2].File.Pieces)
	}
}

// A completed zstd artifact is not a transcript currently being appended. Its
// decoded unterminated final line is part of the source and must not disappear.
func TestRecentCompressedRolloutKeepsEveryDecodedByte(t *testing.T) {
	for _, mode := range []string{"memory", "streamed"} {
		t.Run(mode, func(t *testing.T) {
			e := testenv.New(t)
			if mode == "streamed" {
				syncer.SetLimits(t, 64, 16, 10*time.Minute)
			}
			testenv.Append(t, e.CodexRollout, `{"type":"assistant","message":{"content":"half wr`)
			want := readFile(t, e.CodexRollout)
			artifact := e.CodexRollout + ".zst"
			testenv.Write(t, artifact, string(raw.Compress(want)))
			if err := os.Remove(e.CodexRollout); err != nil {
				t.Fatal(err)
			}
			mustRun(t, "sync", "--quiet", "--agent", "codex", "--path", artifact)
			got := readObject(t, e, fileStream("codex", "rollouts/"+filepath.Base(e.CodexRollout)))
			if !bytes.Equal(got, want) {
				t.Errorf("decoded compressed source = %d bytes, want all %d", len(got), len(want))
			}
		})
	}
}
