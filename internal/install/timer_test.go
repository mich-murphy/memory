package install

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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
"loginctl show-user") echo "Linger=$FAKE_LINGER" ;;
esac
`

func newFakeTools(t *testing.T, linger string) *fakeTools {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"systemctl", "launchctl", "loginctl", "crontab"} {
		testenv.Write(t, filepath.Join(dir, name), fakeToolScript)
		if err := os.Chmod(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_TOOLS_DIR", dir)
	t.Setenv("FAKE_LINGER", linger)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("USER", "tester")
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSystemdWritesUnitsEnablesTimerAndRemovesThem(t *testing.T) {
	e := testenv.New(t)
	tools := newFakeTools(t, "no")
	var log []string
	o := &Options{Command: []string{"/opt/agent sessions/agent-sessions"}, Home: e.Home, Log: &log}
	ctx := t.Context()

	if err := systemd(ctx, o, 15); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.Home, ".config", "systemd", "user")
	service := readFile(t, filepath.Join(dir, "agent-sessions.service"))
	if !strings.Contains(service, "ExecStart='/opt/agent sessions/agent-sessions' sync --quiet --trigger sweep\n") {
		t.Errorf("service doesn't run the quoted command:\n%s", service)
	}
	timer := readFile(t, filepath.Join(dir, "agent-sessions.timer"))
	for _, want := range []string{"every 15 minutes", "OnUnitActiveSec=15min\n", "WantedBy=timers.target\n"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable --now agent-sessions.timer",
		"loginctl show-user tester -p Linger",
	}
	if diff := cmp.Diff(want, tools.calls(t)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if !strings.Contains(strings.Join(log, "\n"), "sudo loginctl enable-linger tester") {
		t.Errorf("no linger advice without linger:\n%s", strings.Join(log, "\n"))
	}

	tools.reset(t)
	o.Remove = true
	if err := systemd(ctx, o, 15); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-sessions.service", "agent-sessions.timer"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still there after removal: %v", name, err)
		}
	}
	want = []string{"systemctl --user disable --now agent-sessions.timer", "systemctl --user daemon-reload"}
	if diff := cmp.Diff(want, tools.calls(t)); diff != "" {
		t.Errorf("removal commands (-want +got):\n%s", diff)
	}

	if err := systemd(ctx, o, 15); err != nil {
		t.Fatalf("removing when already removed: %v", err)
	}
}

func TestSystemdDryRunChangesNothing(t *testing.T) {
	e := testenv.New(t)
	tools := newFakeTools(t, "yes")
	var log []string
	o := &Options{Command: testCmd, Home: e.Home, DryRun: true, Log: &log}
	if err := systemd(t.Context(), o, 15); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".config", "systemd")); !os.IsNotExist(err) {
		t.Errorf("dry run created unit files: %v", err)
	}
	if diff := cmp.Diff([]string{"loginctl show-user tester -p Linger"}, tools.calls(t)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if strings.Contains(strings.Join(log, "\n"), "enable-linger") {
		t.Errorf("linger advice despite linger:\n%s", strings.Join(log, "\n"))
	}
}

func TestLaunchdWritesEscapedPlistLoadsItAndRemovesIt(t *testing.T) {
	e := testenv.New(t)
	tools := newFakeTools(t, "yes")
	var log []string
	o := &Options{Command: []string{"/opt/a&b/agent-sessions"}, Home: e.Home, Log: &log}
	ctx := t.Context()

	if err := launchd(ctx, o, 15); err != nil {
		t.Fatal(err)
	}
	plist := launchdPlistPath(e.Home)
	body := readFile(t, plist)
	for _, want := range []string{
		"<string>/opt/a&amp;b/agent-sessions</string>",
		"<string>sweep</string>",
		"<key>StartInterval</key><integer>900</integer>",
		"<string>" + LaunchdLabel + "</string>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist lacks %q:\n%s", want, body)
		}
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	want := []string{"launchctl bootout " + domain + " " + plist, "launchctl bootstrap " + domain + " " + plist}
	if diff := cmp.Diff(want, tools.calls(t)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}

	tools.reset(t)
	o.Remove = true
	if err := launchd(ctx, o, 15); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Errorf("plist still there after removal: %v", err)
	}
	if diff := cmp.Diff([]string{"launchctl bootout " + domain + " " + plist}, tools.calls(t)); diff != "" {
		t.Errorf("removal commands (-want +got):\n%s", diff)
	}
}

func TestLaunchdDryRunChangesNothing(t *testing.T) {
	e := testenv.New(t)
	tools := newFakeTools(t, "yes")
	var log []string
	o := &Options{Command: testCmd, Home: e.Home, DryRun: true, Log: &log}
	if err := launchd(t.Context(), o, 15); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(launchdPlistPath(e.Home)); !os.IsNotExist(err) {
		t.Errorf("dry run wrote the plist: %v", err)
	}
	if calls := tools.calls(t); calls != nil {
		t.Errorf("dry run ran %q", calls)
	}
}

func TestCronReplacesOnlyItsOwnLine(t *testing.T) {
	tools := newFakeTools(t, "yes")
	other := "0 3 * * * backup.sh"
	testenv.Write(t, filepath.Join(tools.dir, "crontab.txt"),
		other+"\n*/5 * * * * /old/agent-sessions sync "+cronMarker+"\n")
	var log []string
	o := &Options{Command: []string{"/opt/agent sessions/agent-sessions"}, Log: &log}
	ctx := t.Context()

	if err := cron(ctx, o, 15); err != nil {
		t.Fatal(err)
	}
	want := other + "\n*/15 * * * * '/opt/agent sessions/agent-sessions' sync --quiet --trigger sweep >/dev/null 2>&1 " +
		cronMarker + "\n"
	if diff := cmp.Diff(want, tools.crontab(t)); diff != "" {
		t.Errorf("crontab (-want +got):\n%s", diff)
	}

	o.Remove = true
	if err := cron(ctx, o, 15); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(other+"\n", tools.crontab(t)); diff != "" {
		t.Errorf("crontab after removal (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"cron: sweep installed", "cron: sweep removed"}, log); diff != "" {
		t.Errorf("log (-want +got):\n%s", diff)
	}
}

func TestCronStartsATableAndDryRunLeavesItAlone(t *testing.T) {
	tools := newFakeTools(t, "yes")
	var log []string
	o := &Options{Command: testCmd, DryRun: true, Log: &log}
	ctx := t.Context()

	if err := cron(ctx, o, 10); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"crontab -l"}, tools.calls(t)); diff != "" {
		t.Errorf("dry run commands (-want +got):\n%s", diff)
	}

	o.DryRun = false
	if err := cron(ctx, o, 10); err != nil {
		t.Fatal(err)
	}
	want := "*/10 * * * * /opt/agent-sessions/bin/agent-sessions sync --quiet --trigger sweep >/dev/null 2>&1 " +
		cronMarker + "\n"
	if diff := cmp.Diff(want, tools.crontab(t)); diff != "" {
		t.Errorf("crontab (-want +got):\n%s", diff)
	}
}
