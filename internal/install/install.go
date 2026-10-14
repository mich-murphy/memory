// Package install installs and removes the hooks and the Pi extension. It
// replaces only its own entries, backs a file up before changing it, and never rewrites one it
// can't parse.
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

var Ours = regexp.MustCompile(`(?:agent-sessions|agent_sessions).*\bhook (claude|codex|pi)\b`)

type HookEvent struct {
	Name    string
	Async   bool
	Timeout time.Duration
}

// docs/hooks.md says why each event is hooked and how.
var ClaudeEvents = []HookEvent{
	{"SessionStart", true, 30 * time.Second},
	{"Stop", true, 30 * time.Second},
	{"StopFailure", true, 30 * time.Second},
	{"SubagentStop", true, 30 * time.Second},
	{"PreCompact", true, 30 * time.Second},
	{"TaskCompleted", true, 30 * time.Second},
	{"TeammateIdle", true, 30 * time.Second},
	{"SessionEnd", false, 5 * time.Second},
}

var CodexEvents = []HookEvent{
	{"SessionStart", true, 30 * time.Second},
	{"Stop", true, 30 * time.Second},
	{"SubagentStop", true, 30 * time.Second},
	{"PreCompact", true, 30 * time.Second},
	{"Interrupt", true, 3 * time.Second},
	{"SessionEnd", false, 3 * time.Second},
}

type Options struct {
	Command []string
	Home    string
	DryRun  bool
	Remove  bool
	Log     []string
}

func (o *Options) Logf(format string, args ...any) {
	o.Log = append(o.Log, fmt.Sprintf(format, args...))
}

// Command prefers agent-sessions on PATH, whose path survives upgrades, which keeps Codex's
// trust hash for the hook valid.
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

func remove(path string, o *Options) error {
	if o.DryRun {
		o.Logf("would remove: %s", path)
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	o.Logf("removed: %s", path)
	return nil
}

func write(path string, text []byte, o *Options) error {
	old, err := os.ReadFile(path)
	exists := err == nil
	if exists && bytes.Equal(old, text) {
		o.Logf("unchanged: %s", path)
		return nil
	}
	if o.DryRun {
		o.Logf("would write: %s", path)
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
		o.Logf("wrote: %s (backup: %s)", path, filepath.Base(saved))
	} else {
		o.Logf("wrote: %s", path)
	}
	return nil
}

// loadJSON fails rather than return an empty object for a file it can't parse, so the file is
// never overwritten.
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

// replaceOurs puts ours in place of the first of our handlers in groups and drops the rest,
// so Codex's trust keys, which hold each handler's position, still match on a reinstall.
func replaceOurs(groups []any, ours *jsonx.Object) (kept []any, replaced bool) {
	for _, group := range groups {
		g, _ := group.(*jsonx.Object)
		handlers := handlersOf(group)
		var others []any
		for _, h := range handlers {
			switch {
			case !Ours.MatchString(commandOf(h)):
				others = append(others, h)
			case ours != nil && !replaced:
				others = append(others, ours)
				replaced = true
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
	return kept, replaced
}

func mergeHooks(doc *jsonx.Object, events []HookEvent, command string) {
	v, _ := doc.Get("hooks")
	hooks, ok := v.(*jsonx.Object)
	if !ok {
		hooks = jsonx.NewObject()
	}
	ours := map[string]*jsonx.Object{}
	if command != "" {
		for _, e := range events {
			h := jsonx.NewObject()
			h.Set("type", "command")
			h.Set("command", command)
			if e.Async {
				h.Set("async", true)
			}
			h.Set("timeout", int(e.Timeout/time.Second))
			ours[e.Name] = h
		}
	}
	for _, event := range hooks.Keys() {
		v, _ := hooks.Get(event)
		groups, _ := v.([]any)
		kept, replaced := replaceOurs(groups, ours[event])
		if replaced {
			delete(ours, event)
		}
		if len(kept) > 0 {
			hooks.Set(event, kept)
		} else {
			hooks.Delete(event)
		}
	}
	for _, e := range events {
		if h, ok := ours[e.Name]; ok {
			group := jsonx.NewObject()
			group.Set("hooks", []any{h})
			v, _ := hooks.Get(e.Name)
			groups, _ := v.([]any)
			hooks.Set(e.Name, append(groups, group))
		}
	}
	if hooks.Len() > 0 {
		doc.Set("hooks", hooks)
	} else {
		doc.Delete("hooks")
	}
}

func hookCommand(cmd []string, agent string) string {
	return shellwords.Join(append(append([]string{}, cmd...), "hook", agent))
}

func Claude(o *Options) error {
	root := adapters.NewClaude(o.Home).Dir()
	path := filepath.Join(root, "settings.json")
	if o.Remove && !fsutil.Exists(path) {
		return nil
	}
	settings, err := loadJSON(path)
	if err != nil {
		return err
	}
	command := ""
	if !o.Remove {
		command = hookCommand(o.Command, "claude")
	}
	mergeHooks(settings, ClaudeEvents, command)
	if !o.Remove {
		raiseCleanupPeriod(settings, o)
	}
	body, err := jsonx.Indent(settings, 2)
	if err != nil {
		return err
	}
	return write(path, body, o)
}

// keepDays is what cleanupPeriodDays is raised to: Claude Code deletes older transcripts.
const keepDays = 3650

func raiseCleanupPeriod(settings *jsonx.Object, o *Options) {
	v, _ := settings.Get("cleanupPeriodDays")
	current, isInt := int64(0), false
	if n, ok := v.(interface{ Int64() (int64, error) }); ok {
		if i, err := n.Int64(); err == nil {
			current, isInt = i, true
		}
	}
	if isInt && current >= keepDays {
		return
	}
	settings.Set("cleanupPeriodDays", keepDays)
	shown := current
	if shown == 0 {
		shown = 30
	}
	o.Logf("claude: cleanupPeriodDays %d -> %d (transcripts were being deleted after it)", shown, keepDays)
}

func Codex(o *Options) error {
	root := adapters.NewCodex(o.Home).Dir()
	path := filepath.Join(root, "hooks.json")
	if o.Remove && !fsutil.Exists(path) {
		return nil
	}
	doc, err := loadJSON(path)
	if err != nil {
		return err
	}
	command := ""
	if !o.Remove {
		command = hookCommand(o.Command, "codex")
	}
	mergeHooks(doc, CodexEvents, command)
	if o.Remove && doc.Len() == 0 {
		if !o.DryRun {
			if _, err := backup(path); err != nil {
				return err
			}
		}
		return remove(path, o)
	}
	body, err := jsonx.Indent(doc, 2)
	if err != nil {
		return err
	}
	if err := write(path, body, o); err != nil {
		return err
	}
	if !o.Remove {
		o.Logf("codex: open Codex and run /hooks once to trust the agent-sessions hooks. Codex skips new hooks until they're trusted.")
		if tomlHasHookEvents(filepath.Join(root, "config.toml")) {
			o.Logf("codex: config.toml also defines [hooks]; Codex runs both but warns. Moving them into hooks.json silences it.")
		}
	}
	return nil
}
