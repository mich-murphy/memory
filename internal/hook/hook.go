// Package hook is the command each agent runs from its hooks: hand off to a detached sync and
// exit at once.
//
// Rules this follows, from each agent's docs and source (see docs/hooks.md):
//   - Always exit 0 and print nothing. Claude Code's Stop hook treats exit 2 as "keep working",
//     and Codex's synchronous Stop hook fails on any stdout that isn't JSON.
//   - Return within milliseconds. Codex gives SessionEnd hooks 1 s by default (3 s max), Claude
//     Code 1.5 s, and both kill a hook's process group when it times out.
//   - Hand the real work to a process in its own session with stdio detached, so it survives
//     `claude -p` teardown, Codex's process-group kill and Pi exiting, and so the agent isn't
//     left waiting for the hook's stdout to close.
//   - Debounce: Stop fires every turn. If a sync for the same file is already queued, don't
//     queue another; the queued one reads the file after its delay, so it picks up this turn
//     too.
//   - Session start is different: record how the agent was launched (its command line, found
//     by walking up the process tree, and allowlisted environment variables) right here, while
//     the agent is still our ancestor, and hand it to the sync, which snapshots the harness and
//     git state before the first prompt.
package hook

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/jsonx"
)

const (
	// PendingStale is how old a pending marker must be before a new hook takes it over.
	PendingStale = 120 * time.Second
	// DefaultDelay is how long a queued sync waits before reading the file.
	DefaultDelay = 3 * time.Second
	stdinLimit   = 1 << 20
	stdinTimeout = 2 * time.Second
	maxAncestors = 10
	psTimeout    = 2 * time.Second
)

var (
	sessionStartEvents = []string{"SessionStart", "session_start"}
	envPrefixes        = []string{"CLAUDE_CODE_", "CODEX_", "PI_", "ANTHROPIC_DEFAULT_"}
	envNames           = []string{
		"ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "MAX_THINKING_TOKENS", "CLAUDE_CONFIG_DIR",
		"MAX_MCP_OUTPUT_TOKENS", "DISABLE_PROMPT_CACHING",
	}
	secretEnv    = regexp.MustCompile(`(?i)TOKEN|KEY|SECRET|PASSWORD|AUTH|CREDENTIAL|COOKIE`)
	agentMarkers = map[string][]string{
		"claude": {"claude", "@anthropic-ai/claude-code"},
		"codex":  {"codex", "@openai/codex"},
		"pi":     {"pi", "pi-coding-agent"},
	}
)

// Seams the tests replace.
var (
	startSync = spawn
	readProc  = proc
	parentPID = os.Getppid
)

// ReadInput reads the JSON object Claude Code and Codex write to a hook's stdin, giving up after
// a short wait so a hook run by hand doesn't hang.
func ReadInput(f *os.File) map[string]any {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice != 0 {
		return map[string]any{}
	}
	_ = f.SetReadDeadline(time.Now().Add(stdinTimeout)) // fails for regular files, which never block
	data, _ := io.ReadAll(io.LimitReader(f, stdinLimit))
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}
	}
	m, ok := jsonx.Map(data)
	if !ok {
		return map[string]any{}
	}
	return m
}

// PathsFromInput returns transcript_path (the session) and agent_transcript_path (a
// subagent's, from SubagentStop). Codex sends transcript_path: null for sessions it doesn't
// persist.
func PathsFromInput(data map[string]any) []string {
	var out []string
	for _, key := range []string{"transcript_path", "agent_transcript_path"} {
		if v, ok := data[key].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ClaimPending creates a marker for a queued sync of path; "" if one is already queued.
func ClaimPending(stateDir, path string) string {
	pending := filepath.Join(stateDir, "pending")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(path))
	marker := filepath.Join(pending, hex.EncodeToString(sum[:])[:24])
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		st, serr := os.Stat(marker)
		if serr != nil || time.Since(st.ModTime()) < PendingStale {
			return ""
		}
		_ = os.Remove(marker) // left behind by a sync that died; take it over
		f, err = os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return ""
	}
	_, werr := f.WriteString(path)
	if cerr := f.Close(); werr != nil || cerr != nil {
		_ = os.Remove(marker)
		return ""
	}
	return marker
}

// proc returns a process's argv and parent pid: from /proc where there is one (Linux), from ps
// otherwise (macOS).
func proc(ctx context.Context, pid int) ([]string, int, bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	cmdline, err := os.ReadFile(dir + "/cmdline")
	if err != nil {
		return procPS(ctx, pid)
	}
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return nil, 0, false
	}
	ppid, ok := statPPID(stat)
	if !ok {
		return nil, 0, false
	}
	return splitCmdline(cmdline), ppid, true
}

// statPPID reads the parent pid from /proc/<pid>/stat. The command name in parentheses may
// itself hold spaces and parentheses, so fields are counted from the last ')'.
func statPPID(stat []byte) (int, bool) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	return ppid, err == nil
}

// splitCmdline splits /proc/<pid>/cmdline's NUL-separated arguments.
func splitCmdline(data []byte) []string {
	var argv []string
	for a := range strings.SplitSeq(string(data), "\x00") {
		if a != "" {
			argv = append(argv, strings.ToValidUTF8(a, "�"))
		}
	}
	return argv
}

// procPS asks ps. Its argv is the whole command line, since ps can't split it reliably.
func procPS(ctx context.Context, pid int) ([]string, int, bool) {
	ctx, cancel := context.WithTimeout(ctx, psTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "ppid=,args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, 0, false
	}
	ppidStr, args, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil {
		return nil, 0, false
	}
	return []string{strings.TrimSpace(args)}, ppid, true
}

func isAgent(agent string, argv []string) bool {
	var tokens []string
	switch {
	case len(argv) > 1:
		tokens = argv[:min(3, len(argv))]
	case len(argv) == 1: // a macOS ps line: split it roughly, for matching only
		f := strings.Fields(argv[0])
		tokens = f[:min(3, len(f))]
	}
	text := strings.Join(tokens, " ")
	if strings.Contains(text, "agent-sessions") || strings.Contains(text, "agent_sessions") {
		return false
	}
	markers := agentMarkers[agent]
	for _, tok := range tokens {
		first, _, _ := strings.Cut(tok, " ")
		base := first[strings.LastIndexByte(first, '/')+1:]
		if base == markers[0] || strings.HasPrefix(base, markers[0]+"-") {
			return true
		}
		if slices.ContainsFunc(markers[1:], func(m string) bool { return strings.Contains(tok, m) }) {
			return true
		}
	}
	return false
}

// LaunchProcess finds the agent's own command line among this process's ancestors.
func LaunchProcess(ctx context.Context, agent string) map[string]any {
	chain := []string{}
	pid := parentPID()
	for range maxAncestors {
		if pid <= 1 {
			break
		}
		argv, ppid, ok := readProc(ctx, pid)
		if !ok {
			break
		}
		name := "?"
		if len(argv) > 0 {
			name = argv[0][strings.LastIndexByte(argv[0], '/')+1:]
		}
		chain = append(chain, name)
		if isAgent(agent, argv) {
			return map[string]any{"argv": argv, "pid": pid, "ancestry": chain}
		}
		pid = ppid
	}
	return map[string]any{"argv": nil, "ancestry": chain}
}

// givenProcess is argv passed in by the Pi extension (process.argv), which the process table
// can't show.
func givenProcess(argvJSON string) map[string]any {
	if argvJSON == "" {
		return nil
	}
	var argv []string
	if err := jsonx.Decode([]byte(argvJSON), &argv); err != nil || argv == nil {
		return nil
	}
	return map[string]any{"argv": argv, "pid": parentPID(), "via": "extension"}
}

// LaunchEnv returns the allowlisted environment variables, never ones that look like secrets.
func LaunchEnv(environ []string) map[string]any {
	out := map[string]any{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		allowed := slices.Contains(envNames, k) ||
			slices.ContainsFunc(envPrefixes, func(p string) bool { return strings.HasPrefix(k, p) })
		if allowed && !secretEnv.MatchString(k) {
			out[k] = v
		}
	}
	return out
}

// Extra is what a hook was told on its command line (the Pi extension passes cwd, source and
// argv).
type Extra struct {
	Source string
	Cwd    string
	Argv   string
}

func firstString(vals ...any) any {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return nil
}

func recordSessionStart(ctx context.Context, stateDir, agent, path string, data map[string]any, event string, extra Extra) error {
	var hookInput any
	if len(data) > 0 {
		hookInput = data
	}
	process := givenProcess(extra.Argv)
	if process == nil {
		process = LaunchProcess(ctx, agent)
	}
	launch := map[string]any{
		"event":           event,
		"recorded_at":     float64(time.Now().UnixNano()) / 1e9,
		"source":          firstString(data["source"], extra.Source),
		"cwd":             firstString(data["cwd"], extra.Cwd),
		"permission_mode": data["permission_mode"],
		"hook_input":      hookInput,
		"process":         process,
		"env":             LaunchEnv(os.Environ()),
	}
	body, err := jsonx.Compact(launch)
	if err != nil {
		return err
	}
	pending := filepath.Join(stateDir, "pending")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		return err
	}
	launchFile := filepath.Join(pending, "launch-"+rand.Text()+".json")
	f, err := os.OpenFile(launchFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(body)
	if err := errors.Join(werr, f.Close()); err != nil {
		return err
	}
	return startSync(ctx, "sync", "--agent", agent, "--path", path, "--launch", launchFile, "--trigger", "hook:"+event)
}

func spawnSync(ctx context.Context, agent, path, marker, event string) error {
	if event == "" {
		event = agent
	}
	return startSync(ctx, "sync", "--agent", agent, "--path", path, "--delay", strconv.FormatFloat(DefaultDelay.Seconds(), 'f', -1, 64),
		"--pending-marker", marker, "--trigger", "hook:"+event)
}

// spawn starts this binary detached: its own session and process group, so it outlives the
// agent's kill, with stdio on /dev/null.
func spawn(ctx context.Context, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Never cancelled: the sync must outlive this process.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), exe, args...)
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Run handles one hook call. It never fails: capture must not break the agent.
func Run(ctx context.Context, agent string, explicitPaths []string, event string, extra Extra) {
	if os.Getenv("AGENT_SESSIONS_DISABLE") != "" {
		return
	}
	stateDir := config.StateDir()
	data := map[string]any{}
	if len(explicitPaths) == 0 {
		data = ReadInput(os.Stdin)
	}
	paths := explicitPaths
	if len(paths) == 0 {
		paths = PathsFromInput(data)
	}
	if e, ok := data["hook_event_name"].(string); ok {
		event = e
	}
	if slices.Contains(sessionStartEvents, event) {
		if len(paths) > 0 {
			_ = recordSessionStart(ctx, stateDir, agent, paths[0], data, event, extra) // never break the agent
		}
		return
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if marker := ClaimPending(stateDir, path); marker != "" {
			_ = spawnSync(ctx, agent, path, marker, event) // never break the agent
		}
	}
}
