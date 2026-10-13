package install

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/testenv"
)

type fakeTools struct {
	dir string
}

const fakeToolScript = `#!/bin/sh
echo "${0##*/} $*" >> "$FAKE_TOOLS_DIR/calls.log"
case "${0##*/} $1" in
"crontab -l") cat "$FAKE_TOOLS_DIR/crontab.txt" 2>/dev/null || exit 1 ;;
"crontab -") cat > "$FAKE_TOOLS_DIR/crontab.txt" ;;
esac
`

func newFakeTools(t *testing.T) *fakeTools {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"systemctl", "launchctl", "crontab"} {
		testenv.Write(t, filepath.Join(dir, name), fakeToolScript)
		if err := os.Chmod(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_TOOLS_DIR", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", "")
	return &fakeTools{dir: dir}
}

func (f *fakeTools) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func (f *fakeTools) reset(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.dir, "calls.log")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func (f *fakeTools) crontab(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "crontab.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// installOldTimers lays out what an earlier version's install left: a launchd agent, systemd
// units with the timer enabled, and a cron line beside the user's own.
func installOldTimers(t *testing.T, home string, tools *fakeTools, userCron string) {
	t.Helper()
	testenv.Write(t, launchdPlistPath(home), "<plist/>")
	for _, p := range systemdUnits(home)[:2] {
		testenv.Write(t, p, "[Unit]\n")
	}
	link := systemdUnits(home)[2]
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(systemdUnits(home)[1], link); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(tools.dir, "crontab.txt"),
		userCron+"*/10 * * * * /opt/agent-sessions sync --quiet --trigger sweep >/dev/null 2>&1 "+cronMarker+"\n")
}

func TestRemoveOldTimersRemovesEveryKindAndKeepsTheUsersCron(t *testing.T) {
	e := testenv.New(t)
	tools := newFakeTools(t)
	userCron := "0 3 * * * backup.sh\n"
	installOldTimers(t, e.Home, tools, userCron)
	ctx := t.Context()
	if diff := cmp.Diff([]string{"launchd", "systemd", "cron"}, OldTimers(ctx, e.Home)); diff != "" {
		t.Errorf("found (-want +got):\n%s", diff)
	}

	tools.reset(t)
	var log []string
	if err := RemoveOldTimers(ctx, &Options{Home: e.Home, Log: &log}); err != nil {
		t.Fatal(err)
	}
	for _, p := range append([]string{launchdPlistPath(e.Home)}, systemdUnits(e.Home)...) {
		if fsutil.Exists(p) {
			t.Errorf("%s still there", p)
		}
	}
	if diff := cmp.Diff(userCron, tools.crontab(t)); diff != "" {
		t.Errorf("crontab (-want +got):\n%s", diff)
	}
	plist := launchdPlistPath(e.Home)
	want := []string{
		"launchctl bootout gui/" + strconv.Itoa(os.Getuid()) + " " + plist,
		"systemctl --user disable --now agent-sessions.timer",
		"systemctl --user daemon-reload",
		"crontab -l",
		"crontab -",
	}
	if diff := cmp.Diff(want, tools.calls(t)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if len(log) != 3 || !strings.HasPrefix(log[0], "removed: "+plist) {
		t.Errorf("log: %q", log)
	}

	tools.reset(t)
	log = nil
	if found := OldTimers(ctx, e.Home); found != nil {
		t.Errorf("still found %v", found)
	}
	if err := RemoveOldTimers(ctx, &Options{Home: e.Home, Log: &log}); err != nil || log != nil {
		t.Errorf("second removal: %v, log %q", err, log)
	}
}

func TestRemoveOldTimersDryRunChangesNothing(t *testing.T) {
	e := testenv.New(t)
	tools := newFakeTools(t)
	installOldTimers(t, e.Home, tools, "")
	before := tools.crontab(t)
	tools.reset(t)
	var log []string
	if err := RemoveOldTimers(t.Context(), &Options{Home: e.Home, DryRun: true, Log: &log}); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"launchd", "systemd", "cron"}, OldTimers(t.Context(), e.Home)); diff != "" {
		t.Errorf("dry run removed (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before, tools.crontab(t)); diff != "" {
		t.Errorf("crontab (-want +got):\n%s", diff)
	}
	for _, c := range tools.calls(t) {
		if c != "crontab -l" {
			t.Errorf("dry run ran %q", c)
		}
	}
	if len(log) != 3 || !strings.HasPrefix(log[2], "would remove: ") {
		t.Errorf("log: %q", log)
	}
}

func TestRemoveOldTimersReportsAFailedCrontabWrite(t *testing.T) {
	e := testenv.New(t)
	tools := newFakeTools(t)
	testenv.Write(t, filepath.Join(tools.dir, "crontab.txt"), "* * * * * x "+cronMarker+"\n")
	testenv.Write(t, filepath.Join(tools.dir, "crontab"), "#!/bin/sh\n[ \"$1\" = -l ] && cat \"$FAKE_TOOLS_DIR/crontab.txt\" && exit\necho denied >&2; exit 1\n")
	var log []string
	err := RemoveOldTimers(t.Context(), &Options{Home: e.Home, Log: &log})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("err = %v", err)
	}
}
