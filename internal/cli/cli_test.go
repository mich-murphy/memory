package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/cli"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/syncer"
	"github.com/mich-murphy/memory/internal/testenv"
)

func run(args ...string) (code int, stdout, stderr string) {
	return runCtx(context.Background(), args...)
}

func runCtx(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = cli.Main(ctx, args, "1.2.3", cli.IO{Stdout: &out, Stderr: &errOut})
	return code, out.String(), errOut.String()
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(p)
		out[p] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// onlyAgentSessionsOnPath leaves PATH holding just a stand-in agent-sessions, which hooks are
// installed to run, and no agents.
func onlyAgentSessionsOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	testenv.Write(t, filepath.Join(bin, "agent-sessions"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(bin, "agent-sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestInstallDryRunTouchesNothing(t *testing.T) {
	e := testenv.New(t)
	before := snapshotTree(t, e.Home)
	code, out, _ := run("install", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if diff := cmp.Diff(before, snapshotTree(t, e.Home)); diff != "" {
		t.Errorf("dry run changed files:\n%s", diff)
	}
	if !strings.Contains(out, "would write") {
		t.Errorf("output:\n%s", out)
	}
}

func TestInstallSkipsAgentsNotOnThisMachine(t *testing.T) {
	e := testenv.New(t)
	onlyAgentSessionsOnPath(t)
	if err := os.RemoveAll(filepath.Join(e.Home, ".codex")); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("install")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "codex: not used on this machine") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".codex")); !os.IsNotExist(err) {
		t.Error("created ~/.codex")
	}
	ext := install.PiExtensionPath(e.Home)
	if _, err := os.Stat(ext); err != nil {
		t.Error("pi extension not installed after skipping codex")
	}
	if code, out, _ := run("uninstall"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(ext); !os.IsNotExist(err) {
		t.Error("pi extension left behind")
	}
}

func TestHookWithUnknownAgentIsANoop(t *testing.T) {
	testenv.New(t)
	for _, args := range [][]string{{"hook"}, {"hook", "cursor"}, {"hook", "cursor", "--path", "/x"}} {
		if code, out, errOut := run(args...); code != 0 || out != "" || errOut != "" {
			t.Errorf("%v: exit %d, %q %q", args, code, out, errOut)
		}
	}
}

func TestUsageAndVersion(t *testing.T) {
	if code, out, _ := run("--version"); code != 0 || out != "1.2.3\n" {
		t.Errorf("--version: %d %q", code, out)
	}
	if code, _, errOut := run(); code != 2 || !strings.Contains(errOut, "Usage") {
		t.Errorf("no args: %d %q", code, errOut)
	}
	if code, _, _ := run("frobnicate"); code != 2 {
		t.Errorf("unknown command: exit %d", code)
	}
	if code, _, _ := run("sync", "--agent", "cursor"); code != 2 {
		t.Errorf("unknown agent: exit %d", code)
	}
	if code, _, _ := run("sync", "-h"); code != 0 {
		t.Errorf("sync -h: exit %d", code)
	}
}

func TestDoctorReportsWhatIsMissing(t *testing.T) {
	e := testenv.New(t)
	onlyAgentSessionsOnPath(t)
	code, out, _ := run("doctor")
	if code != 1 {
		t.Errorf("exit %d", code)
	}
	for _, want := range []string{
		"[ok  ] machine id: testbox", "[warn] writing to a local directory", "[FAIL] claude: 0/8 agent-sessions hooks",
		"[FAIL] codex: 0/6", "[FAIL] pi: extension missing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	if code, out, _ := run("install"); code != 0 {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	_, out, _ = run("doctor")
	for _, want := range []string{"[ok  ] claude: 8/8", "[ok  ] codex: 6/6", "[FAIL] codex: 0/6 agent-sessions hooks trusted", "[ok  ] pi: extension installed", "[ok  ] claude: cleanupPeriodDays = 3650"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}

	ext := filepath.Join(e.Home, ".pi", "agent", "extensions", "agent-sessions.ts")
	testenv.Write(t, ext, install.PiMarker+"\n// an earlier version\n")
	if _, out, _ = run("doctor"); !strings.Contains(out, "[warn] pi: extension at "+ext+" is out of date") {
		t.Errorf("doctor missed an old extension:\n%s", out)
	}
}

func TestSyncFailsCleanlyWithoutR2Settings(t *testing.T) {
	testenv.New(t)
	t.Setenv("AGENT_SESSIONS_LOCAL_STORE", "")
	for _, k := range []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"} {
		t.Setenv(k, "")
	}
	if code, _, errOut := run("sync"); code != 2 || !strings.Contains(errOut, "R2_") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestASyncThatCannotGetTheLockLeavesTheBucketAlone(t *testing.T) {
	e := testenv.New(t)
	unlock, err := state.Lock(t.Context(), archiveLockDir(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args     []string
		wantCode int
	}{{[]string{"sync"}, 3}, {[]string{"sync", "--quiet"}, 0}} {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		code, out, errOut := runCtx(ctx, c.args...)
		cancel()
		if code != c.wantCode || out != "" {
			t.Errorf("%v while locked: exit %d, stdout %q, stderr %q", c.args, code, out, errOut)
		}
	}
	if keys := e.BucketKeys(); len(keys) != 0 {
		t.Errorf("uploaded while another run held the lock: %v", keys)
	}
	unlock()
	if code, out, _ := run("sync"); code != 0 || len(e.BucketKeys()) == 0 {
		t.Errorf("after unlock: exit %d, %s", code, out)
	}
}

// A session-start hook leaves the launch in a file; the sync it starts puts it in the session's
// manifest and removes the file.
func TestSyncRecordsTheLaunchTheHookLeft(t *testing.T) {
	e := testenv.New(t)
	transcript := filepath.Join(e.PiDir, testenv.PiKey+".jsonl")
	launch := writeLaunch(t, e, "test", transcript)
	code, out, errOut := run("sync", "--agent", "pi", "--path", transcript, "--launch", launch, "--trigger", "hook:session_start")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	record := e.Manifest("pi", testenv.PiSessionKey)["launch"].(map[string]any)
	got := record["envelope"].(map[string]any)["payload"].(map[string]any)
	if got["source"] != "startup" || got["event"] != "session_start" || got["agent"] != nil || got["path"] != nil {
		t.Errorf("manifest launch = %v", got)
	}
	if _, err := os.Stat(launch); !os.IsNotExist(err) {
		t.Errorf("launch file left behind: %v", err)
	}
}

func writeLaunch(t *testing.T, e *testenv.Env, name, transcript string) string {
	t.Helper()
	launch := filepath.Join(e.State, "pending", "launch-"+name+".json")
	testenv.Write(t, launch, `{"event": "session_start", "source": "startup", "cwd": "`+e.Project+`", "recorded_at": 1, `+
		`"agent": "pi", "path": "`+transcript+`"}`)
	return launch
}

// A session-start sync gives up when another run holds the lock for long; its launch is the
// only record of how the session started, so the next run to get the lock records it.
func TestALaunchWhoseSyncCouldNotGetTheLockIsRecordedByTheNextRun(t *testing.T) {
	e := testenv.New(t)
	transcript := filepath.Join(e.PiDir, testenv.PiKey+".jsonl")
	launch := writeLaunch(t, e, "test", transcript)
	testenv.Write(t, filepath.Join(e.State, "pending", "launch-garbage.json"), "not json")
	unlock, err := state.Lock(t.Context(), archiveLockDir(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	code, _, _ := runCtx(ctx, "sync", "--quiet", "--agent", "pi", "--path", transcript, "--launch", launch)
	cancel()
	unlock()
	if code != 0 {
		t.Fatalf("locked sync: exit %d", code)
	}
	if code, out, errOut := run("sync", "--quiet"); code != 1 {
		t.Fatalf("sweep: exit %d\n%s%s", code, out, errOut)
	}
	if got := e.Manifest("pi", testenv.PiSessionKey)["launch"].(map[string]any)["envelope"].(map[string]any)["payload"].(map[string]any); got["source"] != "startup" {
		t.Errorf("manifest launch = %v", got)
	}
	if left, _ := filepath.Glob(filepath.Join(e.State, "pending", "launch-*")); len(left) != 1 || filepath.Base(left[0]) != "launch-garbage.json" {
		t.Errorf("retained failed launch entries: %v", left)
	}
}

func TestQueuedLaunchesAreChronologicalWithStableTies(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.PiDir, testenv.PiKey+".jsonl")
	for _, c := range []struct {
		name   string
		at     float64
		source string
	}{{"a", 30, "late"}, {"b", 10, "first"}, {"c", 20, "middle"}, {"d", 30, "tie"}} {
		body := fmt.Sprintf(`{"schema":1,"id":%q,"agent":"pi","path":%q,"recorded_at":%g,"payload":{"event":"session_start","source":%q}}`, c.name, path, c.at, c.source)
		testenv.Write(t, filepath.Join(e.State, "pending", "launch-"+c.name+".json"), body)
	}
	if code, out, errOut := run("sync", "--agent", "pi"); code != 0 {
		t.Fatalf("sync: %d %s %s", code, out, errOut)
	}
	manifest := e.Manifest("pi", testenv.PiSessionKey)
	launches := manifest["launches"].([]any)
	var got []string
	for _, l := range launches {
		got = append(got, l.(map[string]any)["envelope"].(map[string]any)["payload"].(map[string]any)["source"].(string))
	}
	if diff := cmp.Diff([]string{"first", "middle", "late", "tie"}, got); diff != "" {
		t.Fatalf("launches (-want +got): %s", diff)
	}
}

func TestDisabledSessionRemainsExcludedFromEnabledSweepsAndLaunches(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.PiDir, testenv.PiKey+".jsonl")
	t.Setenv("AGENT_SESSIONS_DISABLE", "1")
	if code, _, _ := run("hook", "pi", "--path", path); code != 0 {
		t.Fatal(code)
	}
	t.Setenv("AGENT_SESSIONS_DISABLE", "")
	queued := writeLaunch(t, e, "excluded", path)
	if code, out, errOut := run("sync", "--agent", "pi"); code != 0 {
		t.Fatalf("sync: %d %s %s", code, out, errOut)
	}
	assertNoArchivedContent(t, e)
	if _, err := os.Stat(queued); !os.IsNotExist(err) {
		t.Fatalf("excluded launch left: %v", err)
	}
	if code, out, errOut := run("sync", "--agent", "pi", "--path", path); code != 0 {
		t.Fatalf("hook sync: %d %s %s", code, out, errOut)
	}
	assertNoArchivedContent(t, e)
}

func TestUnreadablePendingLaunchIsRetainedAndRetried(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.PiDir, testenv.PiKey+".jsonl")
	queued := writeLaunch(t, e, "unreadable", path)
	original, err := os.ReadFile(queued)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(queued, 0); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run("sync", "--agent", "pi"); code != 1 || !strings.Contains(errOut, "launch") {
		t.Fatalf("unreadable sync = %d %s", code, errOut)
	}
	if _, err := os.Stat(queued); err != nil {
		t.Fatalf("pending launch was erased: %v", err)
	}
	if err := os.Chmod(queued, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(queued)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(original), string(got)); diff != "" {
		t.Fatalf("pending bytes changed: %s", diff)
	}
	if code, out, errOut := run("sync", "--agent", "pi"); code != 0 {
		t.Fatalf("retry = %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(queued); !os.IsNotExist(err) {
		t.Fatalf("successful launch queue remains: %v", err)
	}
}

func TestMalformedPendingLaunchPreservesExactBytesAndReportsError(t *testing.T) {
	e := testenv.New(t)
	path := filepath.Join(e.State, "pending", "launch-malformed.json")
	const original = `{"agent":"pi","path":"/p","recorded_at":"bad"}`
	testenv.Write(t, path, original)
	for range 2 {
		if code, _, errOut := run("sync", "--agent", "pi"); code != 1 || !strings.Contains(errOut, "launch") {
			t.Fatalf("malformed sync = %d %s", code, errOut)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("queue bytes = %q", got)
		}
	}
}

func archiveLockDir(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := syncer.LockDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertNoArchivedContent(t *testing.T, e *testenv.Env) {
	t.Helper()
	for _, key := range e.BucketKeys() {
		if strings.Contains(key, "/records/") || strings.HasPrefix(key, "v3/blobs/") || strings.HasPrefix(key, "v3/snapshots/") {
			t.Errorf("excluded session archived content: %s", key)
		}
	}
}
