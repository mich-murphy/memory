package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/fsutil"
)

// Earlier versions also ran a sync every 10 minutes from a launchd agent, a systemd user timer
// or a cron line. Capture now follows session hooks alone, so install, uninstall and doctor
// look for what those versions left behind.
const (
	launchdLabel     = "tech.agent-sessions.sweep"
	systemdTimerUnit = "agent-sessions.timer"
	cronMarker       = "# agent-sessions sweep"
	commandTimeout   = 30 * time.Second
)

type cmdResult struct {
	ok     bool
	stdout string
	stderr string
}

func run(ctx context.Context, stdin, name string, args ...string) cmdResult {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	return cmdResult{ok: err == nil, stdout: out.String(), stderr: strings.TrimSpace(errOut.String())}
}

func launchdPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func systemdUnitDir(home string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user")
}

// systemdUnits lists the service and timer, then the link that enabling the timer made.
func systemdUnits(home string) []string {
	dir := systemdUnitDir(home)
	return []string{
		filepath.Join(dir, "agent-sessions.service"),
		filepath.Join(dir, systemdTimerUnit),
		filepath.Join(dir, "timers.target.wants", systemdTimerUnit),
	}
}

// crontabWithoutSweep returns the user's crontab minus the sweep line, and whether it had one.
func crontabWithoutSweep(ctx context.Context) (table string, found bool) {
	r := run(ctx, "", "crontab", "-l")
	if !r.ok {
		return "", false
	}
	var kept strings.Builder
	for line := range strings.Lines(r.stdout) {
		if strings.Contains(line, cronMarker) {
			found = true
			continue
		}
		kept.WriteString(line)
	}
	return kept.String(), found
}

// OldTimers names each periodic sync an earlier version installed that is still here.
func OldTimers(ctx context.Context, home string) []string {
	var out []string
	if fsutil.Exists(launchdPlistPath(home)) {
		out = append(out, "launchd")
	}
	if slices.ContainsFunc(systemdUnits(home), fsutil.Exists) {
		out = append(out, "systemd")
	}
	if _, found := crontabWithoutSweep(ctx); found {
		out = append(out, "cron")
	}
	return out
}

func RemoveOldTimers(ctx context.Context, o *Options) error {
	return errors.Join(removeLaunchd(ctx, o), removeSystemd(ctx, o), removeCron(ctx, o))
}

func (o *Options) logRemoved(what string) {
	if o.DryRun {
		o.logf("would remove: %s", what)
	} else {
		o.logf("removed: %s", what)
	}
}

func removeLaunchd(ctx context.Context, o *Options) error {
	plist := launchdPlistPath(o.Home)
	if !fsutil.Exists(plist) {
		return nil
	}
	if !o.DryRun {
		run(ctx, "", "launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid()), plist) // fails harmlessly when not loaded
		if err := os.Remove(plist); err != nil {
			return fmt.Errorf("install: %w", err)
		}
	}
	o.logRemoved(plist)
	return nil
}

func removeSystemd(ctx context.Context, o *Options) error {
	units := systemdUnits(o.Home)
	present := slices.DeleteFunc(slices.Clone(units), func(p string) bool { return !fsutil.Exists(p) })
	if len(present) == 0 {
		return nil
	}
	if !o.DryRun {
		// Without a user session to talk to this fails, and removing the files is enough.
		run(ctx, "", "systemctl", "--user", "disable", "--now", systemdTimerUnit)
		for _, p := range present {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("install: %w", err)
			}
		}
		run(ctx, "", "systemctl", "--user", "daemon-reload")
	}
	o.logRemoved(strings.Join(present, ", "))
	return nil
}

func removeCron(ctx context.Context, o *Options) error {
	table, found := crontabWithoutSweep(ctx)
	if !found {
		return nil
	}
	if !o.DryRun {
		if r := run(ctx, table, "crontab", "-"); !r.ok {
			return fmt.Errorf("install: crontab: %s", r.stderr)
		}
	}
	o.logRemoved("the agent-sessions line in your crontab")
	return nil
}
