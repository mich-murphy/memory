package cli_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/cli"
	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/state"
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
	if err := os.RemoveAll(filepath.Join(e.Home, ".pi")); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("install")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "pi: not used on this machine") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".pi")); !os.IsNotExist(err) {
		t.Error("created ~/.pi")
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".codex", "hooks.json")); err != nil {
		t.Error("codex hooks not installed")
	}
	if code, out, _ := run("uninstall"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Error("codex hooks.json left behind")
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
	unlock, err := state.Lock(t.Context(), e.State, time.Second)
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
