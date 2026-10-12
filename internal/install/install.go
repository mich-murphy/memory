// Package install installs and removes the hooks, the Pi extension and the sweep timer.
//
// Every edit is idempotent: agent-sessions' own entries are recognised by their command,
// removed and re-added, and anything else in the file is left alone. A file is backed up before
// it changes and never rewritten if it can't be parsed.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/shellwords"
)

// Ours matches the hook commands agent-sessions writes. It also matches entries written by
// memsync, this tool's earlier name, so a reinstall replaces them.
var Ours = regexp.MustCompile(`(?:agent-sessions|agent_sessions|memsync).*\bhook (claude|codex|pi)\b`)

// ErrNotObject means a settings file holds JSON that isn't an object.
var ErrNotObject = errors.New("does not contain a JSON object")

// HookEvent is one event to hook and the handler settings it gets.
type HookEvent struct {
	Name    string
	Async   bool
	Timeout int
}

// ClaudeEvents are the Claude Code hooks (https://code.claude.com/docs/en/hooks).
//
// SessionStart (startup, resume, clear, compact) records the launch and the harness before the
// first prompt. Stop fires after every turn but not on a user interrupt; StopFailure covers
// turns that end on an API error; SubagentStop carries agent_transcript_path; PreCompact
// catches the transcript before compaction; TaskCompleted and TeammateIdle catch task lists and
// agent-team mailboxes that Claude Code deletes when the team is done; SessionEnd covers
// /clear, /resume, exit and logout. async keeps the hook off Claude's path. SessionEnd stays
// synchronous (it runs during teardown) with a timeout well above the hook's run time.
var ClaudeEvents = []HookEvent{
	{"SessionStart", true, 30},
	{"Stop", true, 30},
	{"StopFailure", true, 30},
	{"SubagentStop", true, 30},
	{"PreCompact", true, 30},
	{"TaskCompleted", true, 30},
	{"TeammateIdle", true, 30},
	{"SessionEnd", false, 5},
}

// CodexEvents are the Codex hooks (codex-rs/hooks, codex-rs/config/src/hook_config.rs).
//
// SessionStart on startup and resume, Stop after every turn, SubagentStop for subagent threads,
// PreCompact before compaction, Interrupt when the user cancels a turn, SessionEnd on exit
// (sync only, 1 s default, clamped to 3 s). Async hooks ignore stdout, which keeps a chatty
// login shell (Codex runs hooks with `$SHELL -lc`) from failing Stop.
var CodexEvents = []HookEvent{
	{"SessionStart", true, 30},
	{"Stop", true, 30},
	{"SubagentStop", true, 30},
	{"PreCompact", true, 30},
	{"Interrupt", true, 3},
	{"SessionEnd", false, 3},
}

// Options are what an install or removal does.
type Options struct {
	// Command is the agent-sessions command line hooks run.
	Command []string
	Home    string
	DryRun  bool
	Remove  bool
	// KeepDays raises Claude Code's cleanupPeriodDays to at least this (0 leaves it).
	KeepDays int
	// Log collects what was done, one line per change.
	Log *[]string
}

func (o *Options) logf(format string, args ...any) {
	*o.Log = append(*o.Log, fmt.Sprintf(format, args...))
}

// Command is the command hooks should run. It prefers agent-sessions on PATH, whose path
// survives upgrades, which keeps Codex's trust hash for the hook valid.
func Command() []string {
	if p, err := exec.LookPath("agent-sessions"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return []string{abs}
		}
	}
	if exe, err := os.Executable(); err == nil {
		return []string{exe}
	}
	return []string{"agent-sessions"}
}

func backup(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	name := path + ".agent-sessions-backup-" + time.Now().Format("20060102-150405")
	if err := fsutil.WriteFileAtomic(name, data, st.Mode().Perm()); err != nil {
		return "", err
	}
	return name, nil
}

// write replaces path with text (backed up first), unless it already holds it.
func write(path string, text []byte, o *Options) error {
	old, err := os.ReadFile(path)
	exists := err == nil
	if exists && bytes.Equal(old, text) {
		o.logf("unchanged: %s", path)
		return nil
	}
	if o.DryRun {
		o.logf("would write: %s", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	perm := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	saved, err := backup(path)
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(path, text, perm); err != nil {
		return err
	}
	if saved != "" {
		o.logf("wrote: %s (backup: %s)", path, filepath.Base(saved))
	} else {
		o.logf("wrote: %s", path)
	}
	return nil
}

// loadJSON reads a JSON object keeping its key order. It fails rather than return an empty
// object for a file it can't parse, so the file is never overwritten.
func loadJSON(path string) (*jsonx.Object, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(data)) == 0) {
		return jsonx.NewObject(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("install: %w", err)
	}
	obj, err := jsonx.ParseObject(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return obj, nil
}

func commandOf(handler any) string {
	h, ok := handler.(*jsonx.Object)
	if !ok {
		return ""
	}
	v, _ := h.Get("command")
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func handlersOf(group any) []any {
	g, ok := group.(*jsonx.Object)
	if !ok {
		return nil
	}
	v, _ := g.Get("hooks")
	handlers, _ := v.([]any)
	return handlers
}

// withoutOurs drops agent-sessions' handlers from an event's groups, and any group left empty
// by that.
func withoutOurs(groups []any) []any {
	var kept []any
	for _, group := range groups {
		g, _ := group.(*jsonx.Object)
		handlers := handlersOf(group)
		var others []any
		for _, h := range handlers {
			if !Ours.MatchString(commandOf(h)) {
				others = append(others, h)
			}
		}
		switch {
		case len(handlers) == 0:
			kept = append(kept, group)
		case len(others) > 0:
			g = g.Clone() // handlers came from it, so it is an object
			g.Set("hooks", others)
			kept = append(kept, g)
		}
	}
	return kept
}

// MergeHooks removes agent-sessions' handlers from every event, then adds one group per event
// unless command is "".
func MergeHooks(config *jsonx.Object, events []HookEvent, command string) {
	v, _ := config.Get("hooks")
	hooks, ok := v.(*jsonx.Object)
	if !ok {
		hooks = jsonx.NewObject()
	}
	for _, event := range hooks.Keys() {
		v, _ := hooks.Get(event)
		groups, _ := v.([]any)
		if kept := withoutOurs(groups); len(kept) > 0 {
			hooks.Set(event, kept)
		} else {
			hooks.Delete(event)
		}
	}
	if command != "" {
		for _, e := range events {
			handler := jsonx.NewObject()
			handler.Set("type", "command")
			handler.Set("command", command)
			if e.Async {
				handler.Set("async", true)
			}
			handler.Set("timeout", e.Timeout)
			group := jsonx.NewObject()
			group.Set("hooks", []any{handler})
			v, _ := hooks.Get(e.Name)
			groups, _ := v.([]any)
			hooks.Set(e.Name, append(groups, group))
		}
	}
	if hooks.Len() > 0 {
		config.Set("hooks", hooks)
	} else {
		config.Delete("hooks")
	}
}

func hookCommand(cmd []string, agent string) string {
	return shellwords.Join(append(append([]string{}, cmd...), "hook", agent))
}

// Claude installs (or removes) the Claude Code hooks in settings.json.
func Claude(o *Options) error {
	root := adapters.NewClaude(o.Home).DefaultRoots()[0]
	path := filepath.Join(root, "settings.json")
	settings, err := loadJSON(path)
	if err != nil {
		return err
	}
	command := ""
	if !o.Remove {
		command = hookCommand(o.Command, "claude")
	}
	MergeHooks(settings, ClaudeEvents, command)
	if !o.Remove && o.KeepDays > 0 {
		raiseCleanupPeriod(settings, o)
	}
	body, err := jsonx.Indent(settings, 2)
	if err != nil {
		return err
	}
	return write(path, body, o)
}

func raiseCleanupPeriod(settings *jsonx.Object, o *Options) {
	v, _ := settings.Get("cleanupPeriodDays")
	current, isInt := int64(0), false
	if n, ok := v.(interface{ Int64() (int64, error) }); ok {
		if i, err := n.Int64(); err == nil {
			current, isInt = i, true
		}
	}
	if isInt && current >= int64(o.KeepDays) {
		return
	}
	settings.Set("cleanupPeriodDays", o.KeepDays)
	shown := current
	if shown == 0 {
		shown = 30
	}
	o.logf("claude: cleanupPeriodDays %d -> %d (transcripts were being deleted after it)", shown, o.KeepDays)
}

// Codex installs (or removes) the Codex hooks in hooks.json.
func Codex(o *Options) error {
	root := adapters.NewCodex(o.Home).DefaultRoots()[0]
	path := filepath.Join(root, "hooks.json")
	config, err := loadJSON(path)
	if err != nil {
		return err
	}
	command := ""
	if !o.Remove {
		command = hookCommand(o.Command, "codex")
	}
	MergeHooks(config, CodexEvents, command)
	if o.Remove && config.Len() == 0 {
		if fsutil.Exists(path) && !o.DryRun {
			if _, err := backup(path); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("install: %w", err)
			}
		}
		o.logf("removed: %s", path)
		return nil
	}
	body, err := jsonx.Indent(config, 2)
	if err != nil {
		return err
	}
	if err := write(path, body, o); err != nil {
		return err
	}
	if !o.Remove {
		o.logf("codex: open Codex and run /hooks once to trust the agent-sessions hooks. Codex skips new hooks until they're trusted.")
		if tomlHasHookEvents(filepath.Join(root, "config.toml")) {
			o.logf("codex: config.toml also defines [hooks]; Codex runs both but warns. Moving them into hooks.json silences it.")
		}
	}
	return nil
}
