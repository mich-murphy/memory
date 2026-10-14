// Package doctor checks everything a silent hook can't report.
package doctor

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/shellwords"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

type level string

const (
	pass level = "ok  "
	warn level = "warn"
	fail level = "FAIL"
)

const (
	keepDaysWanted = 365
	recentRuns     = 20
	runHistory     = 500
)

var errReadBack = errors.New("test object read back differs from what was written")

type reporter struct {
	w      io.Writer
	failed bool
}

func (r *reporter) reportf(l level, format string, args ...any) {
	if l == fail {
		r.failed = true
	}
	_, _ = fmt.Fprintf(r.w, "[%s] %s\n", l, fmt.Sprintf(format, args...)) // a lost line of terminal output can't be reported anywhere
}

func pick(good bool, otherwise level) level {
	if good {
		return pass
	}
	return otherwise
}

func Run(ctx context.Context, cfg *config.Config, w io.Writer) (allPassed bool) {
	r := &reporter{w: w}
	r.reportf(pass, "machine id: %s (hostname %s)", cfg.MachineID, cfg.Hostname)
	checkStore(ctx, cfg, r)
	if os.Getenv("AGENT_SESSIONS_DISABLE") != "" {
		r.reportf(fail, "AGENT_SESSIONS_DISABLE is set, so capture is disabled")
	}
	checkClaude(cfg.Home, r)
	checkCodex(cfg.Home, r)
	checkPi(cfg.Home, r)

	cmd := install.Command()
	_, lookErr := exec.LookPath(cmd[0])
	r.reportf(pick(lookErr == nil || fsutil.Exists(cmd[0]), fail), "agent-sessions command: %s", strings.Join(cmd, " "))

	checkRuns(ctx, cfg, r)
	return !r.failed
}

func checkStore(ctx context.Context, cfg *config.Config, r *reporter) {
	if cfg.LocalStore != "" {
		r.reportf(warn, "writing to a local directory, not R2: %s", cfg.LocalStore)
		return
	}
	if missing := cfg.MissingR2Settings(); len(missing) > 0 {
		r.reportf(fail, "missing %s (set them in %s)", strings.Join(missing, ", "), cfg.Path)
		return
	}
	if err := probeStore(ctx, cfg); err != nil {
		r.reportf(fail, "bucket %s: %v", cfg.R2Bucket, err)
		return
	}
	r.reportf(pass, "bucket %s: write, read and delete work", cfg.R2Bucket)
}

func probeStore(ctx context.Context, cfg *config.Config) error {
	bucket, err := store.Open(cfg)
	if err != nil {
		return err
	}
	key := "_agent-sessions/doctor/" + cfg.MachineID + "/" + rand.Text()
	if err := bucket.Put(ctx, key, []byte("ok"), map[string]string{}); err != nil {
		return err
	}
	body, err := bucket.Get(ctx, key)
	if err != nil {
		return err
	}
	if string(body) != "ok" {
		return errReadBack
	}
	return bucket.Delete(ctx, key)
}

func readJSON(path string) *jsonx.Object {
	data, err := os.ReadFile(path)
	if err != nil {
		return jsonx.NewObject()
	}
	obj, err := jsonx.ParseObject(data)
	if err != nil {
		return jsonx.NewObject()
	}
	return obj
}

func checkClaude(home string, r *reporter) {
	root := adapters.NewClaude(home).Dir()
	if !fsutil.Exists(root) {
		r.reportf(pass, "claude: not used here (%s missing)", root)
		return
	}
	path := filepath.Join(root, "settings.json")
	settings := readJSON(path)
	handlers := install.OurHandlers(settings)
	checkHookCoverage("claude", handlers, install.ClaudeEvents, path, r)
	if v, _ := settings.Get("disableAllHooks"); v == true {
		r.reportf(fail, "claude: disableAllHooks is set, so no hooks run")
	}
	days, set := settings.Get("cleanupPeriodDays")
	if !set {
		days = 30
	}
	good := false
	if n, isNum := days.(interface{ Int64() (int64, error) }); isNum {
		i, err := n.Int64()
		good = err == nil && i >= keepDaysWanted
	}
	r.reportf(pick(good, warn), "claude: cleanupPeriodDays = %v (Claude Code deletes older transcripts)", days)
}

func checkCodex(home string, r *reporter) {
	root := adapters.NewCodex(home).Dir()
	if !fsutil.Exists(root) {
		r.reportf(pass, "codex: not used here (%s missing)", root)
		return
	}
	path := filepath.Join(root, "hooks.json")
	doc := readJSON(path)
	if err := install.ValidateCodexHooks(doc); err != nil {
		r.reportf(fail, "codex: %v; hooks could not be verified", err)
		return
	}
	configured, trusted := install.CodexTrustState(root)
	checkHookCoverage("codex", install.OurHandlers(doc), install.CodexEvents, path, r)
	if configured > 0 {
		hint := ""
		if trusted != configured {
			hint = " (open Codex and review them in /hooks)"
		}
		r.reportf(pick(trusted == configured, fail), "codex: %d/%d agent-sessions hooks trusted%s", trusted, configured, hint)
	}
	if codexHooksDisabled(filepath.Join(root, "config.toml")) {
		r.reportf(fail, "codex: [features] hooks = false in config.toml, so no hooks run")
	}
}

func codexHooksDisabled(path string) bool {
	var doc map[string]any
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return false
	}
	features, _ := doc["features"].(map[string]any)
	return features["hooks"] == false || features["codex_hooks"] == false
}

func checkPi(home string, r *reporter) {
	if dir := adapters.NewPi(home).Dir(); !fsutil.Exists(dir) {
		r.reportf(pass, "pi: not used here (%s missing)", dir)
		return
	}
	ext := install.PiExtensionPath(home)
	data, err := os.ReadFile(ext)
	switch current, _ := install.PiExtension(install.Command()); {
	case err != nil || !bytes.Contains(data, []byte(install.PiMarker)):
		r.reportf(fail, "pi: extension missing at %s", ext)
	case !bytes.Equal(data, current):
		r.reportf(warn, "pi: extension at %s is out of date (run agent-sessions install)", ext)
	default:
		r.reportf(pass, "pi: extension installed at %s", ext)
	}
	if err == nil && bytes.Contains(data, []byte(install.PiMarker)) {
		checkRunnable("pi", install.PiCommand(data), r)
	}
}

// Count distinct required events, not entries: eight Stop handlers cannot cover
// SessionEnd. Only enabled, unconditional command handlers for this agent count.
func checkHookCoverage(agent string, handlers []install.HandlerRef, events []install.HookEvent, path string, r *reporter) {
	covered := map[string]bool{}
	for _, h := range handlers {
		argv, err := shellwords.Split(h.Command)
		if err != nil {
			r.reportf(fail, "%s: hook command %q: %v", agent, h.Command, err)
			continue
		}
		kind, _ := h.Handler.Get("type")
		matcher, _ := h.Group.Get("matcher")
		disabled, _ := h.Handler.Get("disabled")
		if kind != "command" || disabled == true || (matcher != nil && matcher != "" && matcher != "*") {
			r.reportf(fail, "%s: %s handler is not an enabled, unconditional command hook", agent, h.Event)
			continue
		}
		if len(argv) != 3 || argv[1] != "hook" || argv[2] != agent {
			r.reportf(fail, "%s: %s has the wrong hook command: %q", agent, h.Event, h.Command)
			continue
		}
		checkRunnable(agent, argv, r)
		if _, err := exec.LookPath(argv[0]); err == nil {
			covered[h.Event] = true
		}
	}
	var missing []string
	for _, event := range events {
		if !covered[event.Name] {
			missing = append(missing, event.Name)
		}
	}
	r.reportf(pick(len(missing) == 0, fail), "%s: %d/%d agent-sessions hooks in %s (distinct usable required events)", agent, len(events)-len(missing), len(events), path)
	if len(missing) > 0 {
		r.reportf(fail, "%s: missing usable hooks for %s", agent, strings.Join(missing, ", "))
	}
}

// checkRunnable fails a hook whose binary is gone, as when it was installed from a download
// directory since deleted: the hook would exit 127 at every event without a word.
func checkRunnable(agent string, argv []string, r *reporter) {
	if len(argv) == 0 {
		r.reportf(fail, "%s: a hook has no command", agent)
		return
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		r.reportf(fail, "%s: hooks run %s, which can't be run (rerun `agent-sessions install`): %v", agent, argv[0], err)
	}
}

func isSweep(run state.Run) bool {
	return run.Trigger == "backfill" || strings.HasPrefix(run.Trigger, "sweep")
}

func checkRuns(ctx context.Context, cfg *config.Config, r *reporter) {
	var runs []state.Run
	if db, err := state.Open(ctx, cfg.StateDir); err == nil {
		runs, _ = db.LastRuns(runHistory) // no history to show is reported below
		_ = db.Close()                    // read-only use
	}
	var errorMsgs []string
	for _, run := range runs[:min(len(runs), recentRuns)] {
		if msgs, isList := run.Summary["error_messages"].([]any); isList {
			for _, m := range msgs {
				errorMsgs = append(errorMsgs, fmt.Sprint(m))
			}
		}
	}
	if i := slices.IndexFunc(runs, isSweep); i >= 0 {
		age := time.Since(time.Unix(0, int64(runs[i].FinishedAt*1e9)))
		r.reportf(pass, "last sweep (%s) %.0f min ago, %v errors", runs[i].Trigger, age.Minutes(), runs[i].Summary["errors"])
	} else {
		r.reportf(warn, "no sweep in the last %d runs; one runs when a session starts or ends", runHistory)
	}
	if len(errorMsgs) > 0 {
		r.reportf(warn, "recent errors (see %s): %s", filepath.Join(cfg.StateDir, "agent-sessions.log"), errorMsgs[0])
	}
}
