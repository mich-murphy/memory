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
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

type level string

const (
	ok   level = "ok  "
	warn level = "warn"
	fail level = "FAIL"
)

const (
	keepDaysWanted = 365
	recentRuns     = 20
	runHistory     = 500
)

var errNotFound = errors.New("test object not found after writing it")

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
		return ok
	}
	return otherwise
}

func Run(ctx context.Context, cfg *config.Config, w io.Writer) (allPassed bool) {
	r := &reporter{w: w}
	r.reportf(ok, "machine id: %s (hostname %s)", cfg.MachineID, cfg.Hostname)
	checkStore(ctx, cfg, r)
	checkClaude(cfg.Home, r)
	checkCodex(cfg.Home, r)
	checkPi(cfg.Home, r)

	cmd := install.Command()
	_, lookErr := exec.LookPath(cmd[0])
	r.reportf(pick(lookErr == nil || fsutil.Exists(cmd[0]), fail), "agent-sessions command: %s", strings.Join(cmd, " "))

	if old := install.OldTimers(ctx, cfg.Home); len(old) > 0 {
		r.reportf(fail, "an earlier version's periodic sync is still installed (%s); run agent-sessions install to remove it",
			strings.Join(old, ", "))
	}
	checkRuns(ctx, cfg, r)
	return !r.failed
}

func checkStore(ctx context.Context, cfg *config.Config, r *reporter) {
	if cfg.LocalStore != "" {
		r.reportf(warn, "writing to a local directory, not R2: %s", cfg.LocalStore)
		return
	}
	if missing := cfg.MissingR2Settings(); len(missing) > 0 {
		r.reportf(fail, "missing %s (set them in %s)", strings.Join(missing, ", "), cfg.ConfigPath)
		return
	}
	if err := probeStore(ctx, cfg); err != nil {
		r.reportf(fail, "bucket %s: %v", cfg.R2Bucket, err)
		return
	}
	r.reportf(ok, "bucket %s: write, read and delete work", cfg.R2Bucket)
}

func probeStore(ctx context.Context, cfg *config.Config) error {
	st, err := store.Open(cfg)
	if err != nil {
		return err
	}
	key := "_agent-sessions/doctor/" + cfg.MachineID + "/" + rand.Text()
	if err := st.Put(ctx, key, []byte("ok"), map[string]string{}); err != nil {
		return err
	}
	found, err := st.Exists(ctx, key)
	if err != nil {
		return err
	}
	if !found {
		return errNotFound
	}
	return st.Delete(ctx, key)
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
	root := adapters.NewClaude(home).DefaultRoots()[0]
	if !fsutil.Exists(root) {
		r.reportf(ok, "claude: not used here (%s missing)", root)
		return
	}
	path := filepath.Join(root, "settings.json")
	settings := readJSON(path)
	n, want := len(install.OurHandlers(settings)), len(install.ClaudeEvents)
	r.reportf(pick(n >= want, fail), "claude: %d/%d agent-sessions hooks in %s", n, want, path)
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
	root := adapters.NewCodex(home).DefaultRoots()[0]
	if !fsutil.Exists(root) {
		r.reportf(ok, "codex: not used here (%s missing)", root)
		return
	}
	configured, trusted := install.CodexTrustState(root)
	want := len(install.CodexEvents)
	r.reportf(pick(configured >= want, fail), "codex: %d/%d agent-sessions hooks in %s", configured, want,
		filepath.Join(root, "hooks.json"))
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
	if dir := adapters.NewPi(home).AgentDir(); !fsutil.Exists(dir) {
		r.reportf(ok, "pi: not used here (%s missing)", dir)
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
		r.reportf(ok, "pi: extension installed at %s", ext)
	}
}

func isSweep(run state.Run) bool {
	return run.Trigger == "backfill" || strings.HasPrefix(run.Trigger, "sweep")
}

func checkRuns(ctx context.Context, cfg *config.Config, r *reporter) {
	var runs []state.Run
	if st, err := state.Open(ctx, cfg.StateDir); err == nil {
		runs, _ = st.LastRuns(runHistory) // no history to show is reported below
		_ = st.Close()                    // read-only use
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
		r.reportf(ok, "last sweep (%s) %.0f min ago, %v errors", runs[i].Trigger, age.Minutes(), runs[i].Summary["errors"])
	} else {
		r.reportf(warn, "no sweep in the last %d runs; one runs when a session starts or ends", runHistory)
	}
	if len(errorMsgs) > 0 {
		r.reportf(warn, "recent errors (see %s): %s", filepath.Join(cfg.StateDir, "agent-sessions.log"), errorMsgs[0])
	}
}
