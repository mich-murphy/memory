package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/logging"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/syncer"
)

// A hook's sync gives up soon when a backfill holds the lock, so waiting syncs can't pile up;
// the next sweep catches up.
const (
	hookLockTimeout   = 30 * time.Second
	sweepLockTimeout  = 60 * time.Second
	manualLockTimeout = 10 * time.Minute
)

const (
	defaultInterval = 10
	defaultKeepDays = 3650
	statusRuns      = 8
	statusErrors    = 3
)

type syncArgs struct {
	agents      agentList
	paths       stringList
	delay       float64
	marker      string
	launch      string
	trigger     string
	noSnapshots bool
	quiet       bool
}

func parseSync(cmd string, args []string, o IO) (*syncArgs, error) {
	a := &syncArgs{}
	fs := newFlags(cmd, o)
	fs.Var(&a.agents, "agent", "limit to this agent: claude, codex or pi (repeatable)")
	fs.BoolVar(&a.noSnapshots, "no-snapshots", false, "skip harness snapshots")
	if cmd == "sync" {
		fs.Var(&a.paths, "path", "sync this session file and its related files (repeatable)")
		fs.Float64Var(&a.delay, "delay", 0, "wait this many seconds first (lets transcripts flush)")
		fs.StringVar(&a.marker, "pending-marker", "", "internal: the hook's debounce marker")
		fs.StringVar(&a.launch, "launch", "", "internal: launch details recorded by a session-start hook")
		fs.StringVar(&a.trigger, "trigger", "manual", "internal: what started this run")
		fs.BoolVar(&a.quiet, "quiet", false, "print nothing; exit 0 when another run holds the lock")
	}
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	if cmd == "backfill" {
		a.trigger = "backfill"
	}
	return a, nil
}

func (a *syncArgs) lockTimeout() time.Duration {
	switch {
	case len(a.paths) > 0:
		return hookLockTimeout
	case a.trigger == "sweep":
		return sweepLockTimeout
	default:
		return manualLockTimeout
	}
}

func runSync(ctx context.Context, cmd string, args []string, version string, o IO) (int, error) {
	a, err := parseSync(cmd, args, o)
	if err != nil {
		return exitUsage, err
	}
	cfg, err := config.Load(config.FilePath())
	if err != nil {
		return exitFailed, err
	}
	log, closeLog, err := logging.Open(cfg.StateDir, a.quiet)
	if err != nil {
		return exitFailed, err
	}
	defer closeLog()
	started := state.UnixNow()
	if a.delay > 0 {
		select {
		case <-time.After(time.Duration(a.delay * float64(time.Second))):
		case <-ctx.Done():
			return exitFailed, ctx.Err()
		}
	}
	st, err := store.Open(cfg)
	if err != nil {
		log.Error("cannot open store", "error", err)
		if !a.quiet {
			o.errorf("agent-sessions: %v\n", err)
		}
		return exitUsage, nil
	}
	unlock, err := state.Lock(ctx, cfg.StateDir, a.lockTimeout())
	if errors.Is(err, state.ErrLocked) {
		log.Warn("another run holds the lock; the next sweep will catch up")
		if a.quiet {
			return exitOK, nil
		}
		o.errorf("agent-sessions: %v; the next sweep will catch up\n", err)
		return exitLocked, nil
	}
	if err != nil {
		return exitFailed, err
	}
	defer unlock()
	if a.marker != "" {
		// From here on a new hook should queue another sync: this one is about to read the file.
		_ = os.Remove(a.marker) // already gone is fine
	}
	summary, err := syncLocked(ctx, cfg, st, a, version, log, started)
	if err != nil {
		return exitFailed, err
	}
	agents := a.agents.orAll()
	if body, err := jsonx.Compact(summary.Trimmed()); err == nil {
		log.Info("sync done", "agents", strings.Join(agents, ","), "summary", string(body))
	}
	if !a.quiet {
		o.printf("uploaded %d files (%.1f MB compressed), %d unchanged, %d manifests, %d errors\n",
			summary.Uploaded, float64(summary.BytesUploaded)/1e6, summary.Unchanged, summary.Manifests, summary.Errors)
		for _, msg := range summary.Trimmed().ErrorMessages {
			o.errorf("  error: %s\n", msg)
		}
	}
	if summary.Errors > 0 {
		return exitFailed, nil
	}
	return exitOK, nil
}

func syncLocked(ctx context.Context, cfg *config.Config, st store.Store, a *syncArgs, version string, log *slog.Logger,
	started float64,
) (*syncer.Summary, error) {
	s, err := state.Open(ctx, cfg.StateDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := s.Close(); cerr != nil {
			log.Error("closing state", "error", cerr)
		}
	}()
	sy := syncer.New(cfg, st, s, adapters.All(cfg.Home), syncer.Options{
		Trigger: a.trigger, Snapshots: !a.noSnapshots, Logger: log, Version: version,
	})
	if a.launch != "" {
		recordLaunches(ctx, sy, a, log)
	}
	files, err := filesToSync(sy, a)
	if err != nil {
		return nil, err
	}
	summary, err := sy.Run(ctx, files)
	if err != nil {
		return nil, err
	}
	if err := s.RecordRun(started, a.trigger, summary.Trimmed()); err != nil {
		return nil, err
	}
	return summary, nil
}

func recordLaunches(ctx context.Context, sy *syncer.Syncer, a *syncArgs, log *slog.Logger) {
	launch := readLaunch(a.launch)
	if launch == nil || len(a.paths) == 0 {
		return
	}
	for _, agent := range a.agents.orAll() {
		if err := sy.RecordLaunch(ctx, agent, a.paths[0], launch); err != nil {
			sy.Summary.Errors++
			sy.Summary.ErrorMessages = append(sy.Summary.ErrorMessages, fmt.Sprintf("launch %s: %v", agent, err))
			log.Error("recording launch", "agent", agent, "error", err)
		}
	}
}

func filesToSync(sy *syncer.Syncer, a *syncArgs) ([]syncer.Found, error) {
	agents := a.agents.orAll()
	if len(a.paths) == 0 {
		return sy.SweepFiles(agents)
	}
	var files []syncer.Found
	for _, agent := range agents {
		found, err := sy.HookFiles(agent, a.paths)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	return files, nil
}

func readLaunch(path string) map[string]any {
	data, err := os.ReadFile(path)
	_ = os.Remove(path) // one use only; a leftover is harmless
	if err != nil {
		return nil
	}
	m, _ := jsonx.Map(data)
	return m
}

func runInstall(ctx context.Context, args []string, o IO) (int, error) {
	var agents agentList
	fs := newFlags("install", o)
	fs.Var(&agents, "agent", "only this agent: claude, codex or pi (repeatable; default all present)")
	noTimer := fs.Bool("no-timer", false, "don't install the periodic sweep")
	interval := fs.Int("interval", defaultInterval, "sweep interval in minutes")
	keepDays := fs.Int("keep-days", defaultKeepDays, "raise Claude Code's cleanupPeriodDays to at least this (0 leaves it)")
	dryRun := fs.Bool("dry-run", false, "show what would change")
	if err := parse(fs, args); err != nil {
		return exitUsage, err
	}
	var log []string
	opts := &install.Options{
		Command: install.Command(), Home: config.Home(), DryRun: *dryRun, KeepDays: *keepDays, Log: &log,
	}
	failed := false
	if _, err := install.WriteConfigTemplate(opts); err != nil {
		failed = true
		log = append(log, fmt.Sprintf("config: not written: %v", err))
	}
	if !*dryRun {
		dir := filepath.Dir(config.FilePath())
		if id, err := config.MachineUUID(dir); err == nil {
			log = append(log, fmt.Sprintf("machine id: %s (kept in %s)", id, filepath.Join(dir, "machine-id")))
		}
	}
	present := install.AgentsPresent(opts.Home)
	steps := map[string]func(*install.Options) error{"claude": install.Claude, "codex": install.Codex, "pi": install.Pi}
	for _, agent := range agents.orAll() {
		if len(agents) == 0 && !present[agent] {
			log = append(log, agent+": not used on this machine, skipped (rerun `agent-sessions install` after installing it)")
			continue
		}
		if err := steps[agent](opts); err != nil {
			failed = true
			log = append(log, fmt.Sprintf("%s: not changed: %v", agent, err))
		}
	}
	if !*noTimer {
		if err := install.Timer(ctx, opts, *interval); err != nil {
			failed = true
			log = append(log, fmt.Sprintf("timer: not installed: %v", err))
		}
	}
	o.printf("agent-sessions command: %s\n", strings.Join(opts.Command, " "))
	for _, line := range log {
		o.printf("  %s\n", line)
	}
	if !*dryRun {
		o.printf("\nNext: `agent-sessions doctor` to check everything, then `agent-sessions backfill` to upload existing sessions.\n")
	}
	if failed {
		return exitFailed, nil
	}
	return exitOK, nil
}

func runUninstall(ctx context.Context, args []string, o IO) (int, error) {
	var agents agentList
	fs := newFlags("uninstall", o)
	fs.Var(&agents, "agent", "only this agent: claude, codex or pi (repeatable; default all)")
	keepTimer := fs.Bool("keep-timer", false, "leave the periodic sweep installed")
	dryRun := fs.Bool("dry-run", false, "show what would change")
	if err := parse(fs, args); err != nil {
		return exitUsage, err
	}
	var log []string
	opts := &install.Options{Command: install.Command(), Home: config.Home(), DryRun: *dryRun, Remove: true, Log: &log}
	steps := map[string]func(*install.Options) error{"claude": install.Claude, "codex": install.Codex, "pi": install.Pi}
	failed := false
	for _, agent := range agents.orAll() {
		if err := steps[agent](opts); err != nil {
			failed = true
			log = append(log, fmt.Sprintf("%s: not changed: %v", agent, err))
		}
	}
	if !*keepTimer {
		if err := install.Timer(ctx, opts, defaultInterval); err != nil {
			failed = true
			log = append(log, fmt.Sprintf("timer: not removed: %v", err))
		}
	}
	for _, line := range log {
		o.printf("  %s\n", line)
	}
	if failed {
		return exitFailed, nil
	}
	return exitOK, nil
}

func runStatus(ctx context.Context, args []string, o IO) (int, error) {
	if err := parse(newFlags("status", o), args); err != nil {
		return exitUsage, err
	}
	cfg, err := config.Load(config.FilePath())
	if err != nil {
		return exitFailed, err
	}
	s, err := state.Open(ctx, cfg.StateDir)
	if err != nil {
		return exitFailed, err
	}
	defer func() { _ = s.Close() }() // read-only use
	o.printf("machine: %s (%s)   state: %s\n", cfg.MachineID, cfg.Hostname, cfg.StateDir)
	totals, err := s.FileTotals()
	if err != nil {
		return exitFailed, err
	}
	for _, t := range totals {
		o.printf("  %-22s %7d files %10.1f MB\n", t.Agent+"/"+t.Role, t.Files, float64(t.Bytes)/1e6)
	}
	tallies, err := s.Tallies()
	if err != nil {
		return exitFailed, err
	}
	for _, t := range tallies {
		o.printf("  %-22s %7d\n", t.Name, t.Count)
	}
	for _, agent := range adapters.Names {
		roots, err := s.ExtraRoots(agent)
		if err != nil {
			return exitFailed, err
		}
		for _, root := range roots {
			o.printf("  extra %s root: %s\n", agent, root)
		}
	}
	runs, err := s.LastRuns(statusRuns)
	if err != nil {
		return exitFailed, err
	}
	o.printf("recent runs:\n")
	for _, run := range runs {
		when := time.Unix(0, int64(run.FinishedAt*1e9)).Format("2006-01-02 15:04:05")
		o.printf("  %s %-24s up %4v  errors %v\n", when, run.Trigger, run.Summary["uploaded"], run.Summary["errors"])
		msgs, _ := run.Summary["error_messages"].([]any)
		for _, m := range msgs[:min(len(msgs), statusErrors)] {
			o.printf("      %v\n", m)
		}
	}
	return exitOK, nil
}
