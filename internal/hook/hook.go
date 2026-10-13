// Package hook is what each agent's hooks run. It must exit 0, print nothing and return within
// milliseconds, handing the work to a detached sync; docs/hooks.md has each agent's rules.
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
	PendingMarkerStaleAfter = 120 * time.Second
	syncDelay               = 3 * time.Second
	stdinLimit              = 1 << 20
	stdinTimeout            = 2 * time.Second
	maxAncestors            = 10
	sweepKey                = "sweep"
)

var (
	sessionStartEvents = []string{"SessionStart", "session_start"}
	sessionEndEvents   = []string{"SessionEnd", "session_shutdown"}
	envPrefixes        = []string{"CLAUDE_CODE_", "CODEX_", "PI_", "ANTHROPIC_DEFAULT_"}
	envNames           = []string{
		"ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "MAX_THINKING_TOKENS", "CLAUDE_CONFIG_DIR",
		"MAX_MCP_OUTPUT_TOKENS", "DISABLE_PROMPT_CACHING",
	}
	secretEnv    = regexp.MustCompile(`(?i)TOKEN|KEY|SECRET|PASSWORD|AUTH|CREDENTIAL|COOKIE`)
	agentMarkers = map[string]struct{ bin, pkg string }{
		"claude": {"claude", "@anthropic-ai/claude-code"},
		"codex":  {"codex", "@openai/codex"},
		"pi":     {"pi", "pi-coding-agent"},
	}
)

// Seams the tests replace.
var (
	startSync = spawnDetached
	readProc  = proc
	parentPID = os.Getppid
)

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

func PathsFromInput(data map[string]any) []string {
	var out []string
	for _, key := range []string{"transcript_path", "agent_transcript_path"} {
		if v, ok := data[key].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ClaimPending queues at most one sync per key: a transcript's path, or sweepKey.
func ClaimPending(stateDir, key string) (marker string) {
	pending := filepath.Join(stateDir, "pending")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	marker = filepath.Join(pending, hex.EncodeToString(sum[:])[:24])
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		st, serr := os.Stat(marker)
		if serr != nil || time.Since(st.ModTime()) < PendingMarkerStaleAfter {
			return ""
		}
		_ = os.Remove(marker) // left behind by a sync that died; take it over
		f, err = os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return ""
	}
	_, werr := f.WriteString(key)
	if cerr := f.Close(); werr != nil || cerr != nil {
		_ = os.Remove(marker)
		return ""
	}
	return marker
}

func cleanArgs(args []string) []string {
	var argv []string
	for _, a := range args {
		if a != "" {
			argv = append(argv, strings.ToValidUTF8(a, "\uFFFD"))
		}
	}
	return argv
}

func isAgent(agent string, argv []string) bool {
	var tokens []string
	switch {
	case len(argv) > 1:
		tokens = argv[:min(3, len(argv))]
	case len(argv) == 1: // a retitled process shows its whole command line as one string
		f := strings.Fields(argv[0])
		tokens = f[:min(3, len(f))]
	}
	text := strings.Join(tokens, " ")
	if strings.Contains(text, "agent-sessions") || strings.Contains(text, "agent_sessions") {
		return false
	}
	m := agentMarkers[agent]
	return slices.ContainsFunc(tokens, func(tok string) bool {
		first, _, _ := strings.Cut(tok, " ")
		base := first[strings.LastIndexByte(first, '/')+1:]
		return base == m.bin || strings.HasPrefix(base, m.bin+"-") || strings.Contains(tok, m.pkg)
	})
}

func LaunchProcess(agent string) map[string]any {
	chain := []string{}
	pid := parentPID()
	for range maxAncestors {
		if pid <= 1 {
			break
		}
		argv, ppid, ok := readProc(pid)
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

func processFromExtension(argvJSON string) map[string]any {
	if argvJSON == "" {
		return nil
	}
	var argv []string
	if err := jsonx.Decode([]byte(argvJSON), &argv); err != nil || argv == nil {
		return nil
	}
	return map[string]any{"argv": argv, "pid": parentPID(), "via": "extension"}
}

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

type ExtensionArgs struct {
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

func recordSessionStart(ctx context.Context, stateDir, agent, path string, data map[string]any, event string,
	extra ExtensionArgs,
) error {
	var hookInput any
	if len(data) > 0 {
		hookInput = data
	}
	process := processFromExtension(extra.Argv)
	if process == nil {
		process = LaunchProcess(agent)
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
	return startSync(ctx, "sync", "--agent", agent, "--path", path, "--delay", strconv.FormatFloat(syncDelay.Seconds(), 'f', -1, 64),
		"--pending-marker", marker, "--trigger", "hook:"+event)
}

// The delay lets a session-start sync take the lock first.
func spawnSweep(ctx context.Context, stateDir, event string) error {
	marker := ClaimPending(stateDir, sweepKey)
	if marker == "" {
		return nil
	}
	return startSync(ctx, "sync", "--quiet", "--delay", strconv.FormatFloat(syncDelay.Seconds(), 'f', -1, 64),
		"--pending-marker", marker, "--trigger", "sweep:"+event)
}

func spawnDetached(ctx context.Context, args ...string) error {
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

func Run(ctx context.Context, agent string, explicitPaths []string, event string, extra ExtensionArgs) {
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
		_ = spawnSweep(ctx, stateDir, event) // never break the agent
		return
	}
	if slices.Contains(sessionEndEvents, event) {
		_ = spawnSweep(ctx, stateDir, event) // never break the agent
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
