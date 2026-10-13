package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/shellwords"
)

// PiMarker is the first line of the Pi extension agent-sessions manages.
const PiMarker = "// agent-sessions: managed by `agent-sessions install`"

const piExtension = `%s; edits are overwritten.
// Ships this Pi session to the agent-sessions store. agent-sessions hands the upload to a detached process,
// so these handlers return immediately. Docs: https://github.com/mich-murphy/memory
import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const AGENT_SESSIONS: string[] = %s;

function kick(file: string | undefined, extra: string[] = []): void {
	// No file for --no-session runs; Pi also creates the file only after the first message.
	if (!file || process.env.AGENT_SESSIONS_DISABLE) return;
	try {
		const child = spawn(AGENT_SESSIONS[0], [...AGENT_SESSIONS.slice(1), "hook", "pi", "--path", file, ...extra], {
			detached: true,
			stdio: "ignore",
		});
		child.on("error", () => {});
		child.unref();
	} catch {
		// Capture must never break the session.
	}
}

export default function (pi: ExtensionAPI) {
	// Startup, /new, /resume, /fork, /reload: record the launch and the harness before the first prompt.
	// Pi renames its process, which hides its arguments from the process table, so pass them along.
	pi.on("session_start", (event, ctx) =>
		kick(ctx.sessionManager.getSessionFile(), [
			"--event", "session_start", "--source", event.reason, "--cwd", ctx.cwd, "--argv", JSON.stringify(process.argv),
		]),
	);
	// A prompt has fully finished: no retries, compaction or queued follow-ups left.
	pi.on("agent_settled", (_event, ctx) => kick(ctx.sessionManager.getSessionFile()));
	// Quit (including SIGTERM/SIGHUP), /new, /resume, /fork and /reload: flush the session being left.
	pi.on("session_shutdown", (_event, ctx) => kick(ctx.sessionManager.getSessionFile()));
}
`

const systemdService = `[Unit]
Description=agent-sessions sweep: upload new coding-agent session files

[Service]
Type=oneshot
ExecStart=%s sync --quiet --trigger sweep
Nice=10
IOSchedulingClass=idle
`

const systemdTimer = `[Unit]
Description=Run the agent-sessions sweep every %d minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=%dmin
Persistent=true

[Install]
WantedBy=timers.target
`

// LaunchdLabel names the macOS sweep job.
const LaunchdLabel = "tech.agent-sessions.sweep"

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
%s
  </array>
  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><true/>
  <key>LowPriorityIO</key><true/>
  <key>Nice</key><integer>10</integer>
  <key>StandardOutPath</key><string>/dev/null</string>
  <key>StandardErrorPath</key><string>/dev/null</string>
</dict>
</plist>
`

const cronMarker = "# agent-sessions sweep"

const configTemplate = `# agent-sessions settings. Hooks and timers don't load your shell profile, so they read this file.
# Real environment variables override it.
R2_ENDPOINT=%s
R2_BUCKET=%s
R2_ACCESS_KEY_ID=%s
R2_SECRET_ACCESS_KEY=%s
# This machine's id in object keys is a UUID kept in machine-id next to this file.
# Minimum minutes between uploads of a changed SQLite database (Codex's state and logs).
# AGENT_SESSIONS_DB_INTERVAL_MINUTES=60
`

const commandTimeout = 30 * time.Second

// Pi installs (or removes) the Pi extension.
func Pi(o *Options) error {
	path := filepath.Join(adapters.NewPi(o.Home).AgentDir(), "extensions", "agent-sessions.ts")
	if data, err := os.ReadFile(path); err == nil && !bytes.Contains(data, []byte(PiMarker)) {
		o.logf("pi: %s exists and isn't agent-sessions'; leaving it alone", path)
		return nil
	}
	if o.Remove {
		if fsutil.Exists(path) && !o.DryRun {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("install: %w", err)
			}
		}
		o.logf("removed: %s", path)
		return nil
	}
	command, err := jsonx.Compact(o.Command)
	if err != nil {
		return err
	}
	if err := write(path, fmt.Appendf(nil, piExtension, PiMarker, command), o); err != nil {
		return err
	}
	o.logf("pi: new Pi sessions load the extension; run /reload in open ones.")
	return nil
}

type cmdResult struct {
	ok     bool
	stdout string
	stderr string
}

func run(ctx context.Context, stdin, name string, args ...string) cmdResult {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	return cmdResult{ok: err == nil, stdout: out.String(), stderr: strings.TrimSpace(errOut.String())}
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func systemdUserAvailable(ctx context.Context) bool {
	return runtime.GOOS == "linux" && have("systemctl") && run(ctx, "", "systemctl", "--user", "show-environment").ok
}

func sweepArgs(cmd []string) []string {
	return append(append([]string{}, cmd...), "sync", "--quiet", "--trigger", "sweep")
}

func launchdPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist")
}

// Timer installs (or removes) the periodic sweep: launchd on macOS, a systemd user timer where
// there is a user session, cron otherwise.
func Timer(ctx context.Context, o *Options, minutes int) error {
	minutes = max(1, min(minutes, 59))
	switch {
	case runtime.GOOS == "darwin":
		return launchd(ctx, o, minutes)
	case systemdUserAvailable(ctx):
		return systemd(ctx, o, minutes)
	case have("crontab"):
		return cron(ctx, o, minutes)
	}
	o.logf("timer: no systemd user session, launchd or crontab found; run `agent-sessions sync` periodically yourself")
	return nil
}

func launchd(ctx context.Context, o *Options, minutes int) error {
	plist := launchdPlistPath(o.Home)
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if !o.DryRun {
		run(ctx, "", "launchctl", "bootout", domain, plist) // fails harmlessly when not loaded
	}
	if o.Remove {
		if fsutil.Exists(plist) && !o.DryRun {
			if err := os.Remove(plist); err != nil {
				return fmt.Errorf("install: %w", err)
			}
		}
		o.logf("removed: %s", plist)
		return nil
	}
	var args []string
	for _, a := range sweepArgs(o.Command) {
		args = append(args, "    <string>"+html.EscapeString(a)+"</string>")
	}
	body := fmt.Appendf(nil, launchdPlist, LaunchdLabel, strings.Join(args, "\n"), minutes*60)
	if err := write(plist, body, o); err != nil {
		return err
	}
	if !o.DryRun {
		if r := run(ctx, "", "launchctl", "bootstrap", domain, plist); r.ok {
			o.logf("launchd: sweep loaded")
		} else {
			o.logf("launchd: bootstrap failed: %s", r.stderr)
		}
	}
	return nil
}

func systemdUnitDir(home string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user")
}

func systemd(ctx context.Context, o *Options, minutes int) error {
	dir := systemdUnitDir(o.Home)
	service, timer := filepath.Join(dir, "agent-sessions.service"), filepath.Join(dir, "agent-sessions.timer")
	if o.Remove {
		if !o.DryRun {
			run(ctx, "", "systemctl", "--user", "disable", "--now", "agent-sessions.timer")
			for _, p := range []string{service, timer} {
				if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("install: %w", err)
				}
			}
			run(ctx, "", "systemctl", "--user", "daemon-reload")
		}
		o.logf("removed: %s, %s", service, timer)
		return nil
	}
	if err := write(service, fmt.Appendf(nil, systemdService, shellwords.Join(o.Command)), o); err != nil {
		return err
	}
	if err := write(timer, fmt.Appendf(nil, systemdTimer, minutes, minutes), o); err != nil {
		return err
	}
	if !o.DryRun {
		run(ctx, "", "systemctl", "--user", "daemon-reload")
		if r := run(ctx, "", "systemctl", "--user", "enable", "--now", "agent-sessions.timer"); r.ok {
			o.logf("systemd: agent-sessions.timer enabled")
		} else {
			o.logf("systemd: enable failed: %s", r.stderr)
		}
	}
	if !LingerEnabled(ctx) {
		user := os.Getenv("USER")
		if user == "" {
			user = "$USER"
		}
		o.logf("systemd: user timers stop when you log out. To keep sweeping while logged out "+
			"(e.g. agents left running in tmux on a VM), run: sudo loginctl enable-linger %s", user)
	}
	return nil
}

func cron(ctx context.Context, o *Options, minutes int) error {
	var lines []string
	if r := run(ctx, "", "crontab", "-l"); r.ok {
		for line := range strings.Lines(r.stdout) {
			if line = strings.TrimRight(line, "\n"); !strings.Contains(line, cronMarker) {
				lines = append(lines, line)
			}
		}
	}
	if !o.Remove {
		lines = append(lines, fmt.Sprintf("*/%d * * * * %s >/dev/null 2>&1 %s", minutes,
			shellwords.Join(sweepArgs(o.Command)), cronMarker))
	}
	if o.DryRun {
		o.logf("would update crontab")
		return nil
	}
	r := run(ctx, strings.Join(lines, "\n")+"\n", "crontab", "-")
	switch {
	case !r.ok:
		o.logf("cron: failed: %s", r.stderr)
	case o.Remove:
		o.logf("cron: sweep removed")
	default:
		o.logf("cron: sweep installed")
	}
	return nil
}

// LingerEnabled reports whether systemd keeps this user's timers running after logout (true
// when it can't tell, so as not to nag).
func LingerEnabled(ctx context.Context) bool {
	user := os.Getenv("USER")
	if user == "" || !have("loginctl") {
		return true
	}
	r := run(ctx, "", "loginctl", "show-user", user, "-p", "Linger")
	return !r.ok || strings.Contains(r.stdout, "Linger=yes")
}

// TimerInstalled names the sweep timer that is set up ("launchd", "systemd", "cron"), or "".
func TimerInstalled(ctx context.Context, home string) string {
	if runtime.GOOS == "darwin" {
		if fsutil.Exists(launchdPlistPath(home)) {
			return "launchd"
		}
		return ""
	}
	if systemdUserAvailable(ctx) && run(ctx, "", "systemctl", "--user", "is-enabled", "agent-sessions.timer").ok {
		return "systemd"
	}
	if have("crontab") {
		if r := run(ctx, "", "crontab", "-l"); r.ok && strings.Contains(r.stdout, cronMarker) {
			return "cron"
		}
	}
	return ""
}

// WriteConfigTemplate writes the config file from the environment's R2 settings, unless one
// exists.
func WriteConfigTemplate(o *Options) (string, error) {
	path := config.FilePath()
	if fsutil.Exists(path) {
		o.logf("config: keeping %s", path)
		return path, nil
	}
	names := []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"}
	values := make([]any, len(names))
	var missing []string
	for i, k := range names {
		values[i] = os.Getenv(k)
		if values[i] == "" {
			missing = append(missing, k)
		}
	}
	if o.DryRun {
		o.logf("would write: %s", path)
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	_, werr := fmt.Fprintf(f, configTemplate, values...)
	if err := errors.Join(werr, f.Close()); err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if len(missing) > 0 {
		o.logf("config: wrote %s; fill in %s", path, strings.Join(missing, ", "))
	} else {
		o.logf("config: wrote %s from your environment", path)
	}
	return path, nil
}

// codexEventKey is Codex's snake_case event label (codex-rs/hooks/src/lib.rs
// hook_event_key_label).
func codexEventKey(event string) string {
	var b strings.Builder
	for i, r := range event {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func tomlHooks(path string) map[string]any {
	var doc map[string]any
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return nil
	}
	hooks, _ := doc["hooks"].(map[string]any)
	return hooks
}

func tomlHasHookEvents(path string) bool {
	for k := range tomlHooks(path) {
		if k != "state" {
			return true
		}
	}
	return false
}

// HandlerRef locates one hook handler: its event, and its group's and its own index.
type HandlerRef struct {
	Event   string
	Group   int
	Handler int
}

// OurHandlers lists agent-sessions' handlers in a Claude Code settings or Codex hooks file.
func OurHandlers(settings *jsonx.Object) []HandlerRef {
	v, _ := settings.Get("hooks")
	hooks, isObj := v.(*jsonx.Object)
	if !isObj {
		return nil
	}
	var out []HandlerRef
	for _, event := range hooks.Keys() {
		gv, _ := hooks.Get(event)
		groups, _ := gv.([]any)
		for gi, group := range groups {
			for hi, h := range handlersOf(group) {
				if Ours.MatchString(commandOf(h)) {
					out = append(out, HandlerRef{event, gi, hi})
				}
			}
		}
	}
	return out
}

// CodexTrustState counts agent-sessions' Codex hooks and how many of them have a trusted hash
// recorded in config.toml, under keys like <hooks.json path>:<event>:<group>:<handler>.
func CodexTrustState(root string) (configured, trusted int) {
	hooksPath := filepath.Join(root, "hooks.json")
	cfg, err := loadJSON(hooksPath)
	if err != nil {
		return 0, 0
	}
	refs := OurHandlers(cfg)
	state, _ := tomlHooks(filepath.Join(root, "config.toml"))["state"].(map[string]any)
	for _, r := range refs {
		key := fmt.Sprintf("%s:%s:%d:%d", hooksPath, codexEventKey(r.Event), r.Group, r.Handler)
		entry, _ := state[key].(map[string]any)
		if h, _ := entry["trusted_hash"].(string); h != "" {
			trusted++
		}
	}
	return len(refs), trusted
}

// AgentsPresent lists the agents with a config dir or a command on PATH.
func AgentsPresent(home string) map[string]bool {
	dirs := map[string]string{
		"claude": adapters.NewClaude(home).DefaultRoots()[0],
		"codex":  adapters.NewCodex(home).DefaultRoots()[0],
		"pi":     adapters.NewPi(home).AgentDir(),
	}
	out := map[string]bool{}
	for name, d := range dirs {
		if fsutil.Exists(d) || have(name) {
			out[name] = true
		}
	}
	return out
}
