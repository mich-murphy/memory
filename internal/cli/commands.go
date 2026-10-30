package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/logging"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
	"github.com/mich-murphy/memory/internal/syncer"
)

// A hook's sync gives up soon when a backfill holds the lock, so waiting syncs can't pile up;
// the next hook or sweep catches up.
const (
	hookLockTimeout   = 30 * time.Second
	sweepLockTimeout  = 60 * time.Second
	manualLockTimeout = 10 * time.Minute
)

type syncFlags struct {
	agents       agentList
	paths        []string
	delaySeconds float64
	marker       string
	trigger      string
	quiet        bool
}

func parseSync(cmd string, args []string, o IO) (*syncFlags, error) {
	flags := &syncFlags{}
	fs := newFlags(cmd, o)
	fs.Var(&flags.agents, "agent", "limit to this agent: claude, codex or pi (repeatable)")
	if cmd == "sync" {
		fs.Func("path", "sync this session file and its related files (repeatable)", func(p string) error {
			flags.paths = append(flags.paths, p)
			return nil
		})
		fs.Float64Var(&flags.delaySeconds, "delay", 0, "wait this many seconds first (lets transcripts flush)")
		fs.StringVar(&flags.marker, "pending-marker", "", "internal: the hook's debounce marker")
		fs.String("launch", "", "internal: the launch a session-start hook left; every sync records all of them")
		fs.StringVar(&flags.trigger, "trigger", "manual", "internal: what started this run")
		fs.BoolVar(&flags.quiet, "quiet", false, "print nothing; exit 0 when another run holds the lock")
	}
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	if cmd == "backfill" {
		flags.trigger = "backfill"
	}
	return flags, nil
}

func (f *syncFlags) lockTimeout() time.Duration {
	switch {
	case len(f.paths) > 0:
		return hookLockTimeout
	case strings.HasPrefix(f.trigger, "sweep:"):
		return sweepLockTimeout
	default:
		return manualLockTimeout
	}
}

func runSync(ctx context.Context, cmd string, args []string, version string, o IO) (int, error) {
	flags, err := parseSync(cmd, args, o)
	if err != nil {
		return exitUsage, err
	}
	cfg, err := config.Load(config.FilePath())
	if err != nil {
		return exitFailed, err
	}
	log, closeLog, err := logging.Open(cfg.StateDir, flags.quiet)
	if err != nil {
		return exitFailed, err
	}
	defer closeLog()
	started := state.UnixNow()
	if flags.delaySeconds > 0 {
		select {
		case <-time.After(time.Duration(flags.delaySeconds * float64(time.Second))):
		case <-ctx.Done():
			return exitFailed, ctx.Err()
		}
	}
	bucket, err := store.Open(cfg)
	if err != nil {
		log.Error("cannot open store", "error", err)
		if !flags.quiet {
			o.errorf("agent-sessions: %v\n", err)
		}
		return exitUsage, nil
	}
	unlock, err := lockSync(ctx, cfg, flags.lockTimeout())
	if errors.Is(err, state.ErrLocked) {
		log.Warn("another run holds the lock; a later hook will catch up")
		if flags.quiet {
			return exitOK, nil
		}
		o.errorf("agent-sessions: %v; a later hook will catch up\n", err)
		return exitLocked, nil
	}
	if err != nil {
		return exitFailed, err
	}
	defer unlock()
	agents := flags.agents.orAll()
	log.Info("sync start", "trigger", flags.trigger, "agents", strings.Join(agents, ","), "paths", len(flags.paths))
	if flags.marker != "" {
		// From here on a new hook should queue another sync: this one is about to read the file.
		_ = os.Remove(flags.marker) // already gone is fine
	}
	summary, err := syncLocked(ctx, cfg, bucket, flags, version, log, started)
	if err != nil {
		log.Error("sync failed", "error", err, "trigger", flags.trigger)
		return exitFailed, err
	}
	if !flags.quiet {
		o.printf("uploaded %d files (%.1f MB compressed), %d unchanged, %d manifests, %d errors\n",
			summary.Uploaded, float64(summary.BytesUploaded)/1e6, summary.Unchanged, summary.Manifests, summary.Errors)
		for _, msg := range summary.Trimmed().ErrorMessages {
			o.errorf("  error: %s\n", msg)
		}
	}
	if summary.Errors > 0 {
		log.Error("sync failed", "error", strings.Join(summary.Trimmed().ErrorMessages, "; "), "trigger", flags.trigger)
		return exitFailed, nil
	}
	if body, err := jsonx.Compact(summary.Trimmed()); err == nil {
		log.Info("sync done", "agents", strings.Join(agents, ","), "summary", string(body))
	}
	return exitOK, nil
}

func syncLocked(ctx context.Context, cfg *config.Config, bucket store.Store, flags *syncFlags, version string, log *slog.Logger,
	started float64,
) (summary *syncer.Summary, err error) {
	db, err := state.Open(ctx, cfg.StateDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			log.Error("closing state", "error", cerr)
		}
	}()
	sy := syncer.New(cfg, bucket, db, adapters.All(cfg.Home), syncer.Options{
		Trigger: flags.trigger, Logger: log, Version: version,
	})
	summary = &sy.Summary
	defer func() {
		if err != nil {
			summary.Errors++
			summary.ErrorMessages = append(summary.ErrorMessages, err.Error())
		}
		if recordErr := db.RecordRun(started, flags.trigger, summary.Trimmed()); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
	}()
	if err := sy.Recover(ctx); err != nil {
		return &sy.Summary, err
	}
	excluded, err := exclusion.Load(cfg.StateDir)
	if err != nil {
		return &sy.Summary, err
	}
	recordLaunches(ctx, sy, cfg.StateDir, excluded, adapters.All(cfg.Home), log)
	files, err := filesToSync(ctx, sy, flags)
	if err != nil {
		return summary, err
	}
	files = slices.DeleteFunc(files, excluded.Excludes)
	return sy.Run(ctx, files)
}

type pendingLaunch struct {
	file     string
	envelope launch.Envelope
}

// recordLaunches records every launch session-start hooks left, oldest first, not only this
// run's: a sync that never got the lock leaves its launch for the next one that does. A launch
// that fails to record stays for the next run.
func recordLaunches(ctx context.Context, sy *syncer.Syncer, stateDir string, excluded exclusion.Registry, ads map[string]adapters.Adapter, log *slog.Logger) {
	files, _ := filepath.Glob(filepath.Join(stateDir, "pending", "launch-*.json")) // the pattern is valid
	var launches []pendingLaunch
	for _, f := range files {
		l, err := readLaunch(f)
		if err != nil {
			sy.Summary.Errors++
			sy.Summary.ErrorMessages = append(sy.Summary.ErrorMessages, fmt.Sprintf("launch %s: %v", f, err))
			log.Error("pending launch retained for retry", "file", f, "error", err)
			continue
		}
		launches = append(launches, l)
	}
	slices.SortStableFunc(launches, func(a, b pendingLaunch) int {
		return cmp.Compare(a.envelope.RecordedAt, b.envelope.RecordedAt)
	})
	for _, l := range launches {
		e := l.envelope
		if excluded.ExcludesPath(ads[e.Agent], e.Path) {
			removeLaunch(l.file, log)
			continue
		}
		if err := sy.RecordEnvelope(ctx, e); err != nil {
			sy.Summary.Errors++
			sy.Summary.ErrorMessages = append(sy.Summary.ErrorMessages, fmt.Sprintf("launch %s: %v", e.Agent, err))
			log.Error("recording launch", "agent", e.Agent, "error", err)
			continue
		}
		removeLaunch(l.file, log)
	}
}

func readLaunch(file string) (pendingLaunch, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return pendingLaunch{}, err
	}
	envelope, err := launch.Decode(data)
	if err != nil {
		return pendingLaunch{}, err
	}
	return pendingLaunch{file: file, envelope: envelope}, nil
}

func removeLaunch(file string, log *slog.Logger) {
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Error("a recorded launch could not be removed, so it will be recorded again", "file", file, "error", err)
	}
}

func filesToSync(ctx context.Context, sy *syncer.Syncer, flags *syncFlags) ([]adapters.SessionFile, error) {
	agents := flags.agents.orAll()
	if len(flags.paths) == 0 {
		return sy.SweepFiles(ctx, agents)
	}
	var files []adapters.SessionFile
	for _, agent := range agents {
		found, err := sy.HookFilesContext(ctx, agent, flags.paths)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	return files, nil
}

var installers = map[string]func(*install.Options) error{"claude": install.Claude, "codex": install.Codex, "pi": install.Pi}

func runInstall(args []string, o IO) (int, error) {
	var agents agentList
	fs := newFlags("install", o)
	fs.Var(&agents, "agent", "only this agent: claude, codex or pi (repeatable; default all present)")
	dryRun := fs.Bool("dry-run", false, "show what would change")
	if err := parse(fs, args); err != nil {
		return exitUsage, err
	}
	opts := &install.Options{Command: install.Command(), Home: config.Home(), DryRun: *dryRun}
	failed := false
	if _, err := install.WriteConfigTemplate(opts); err != nil {
		failed = true
		opts.Logf("config: not written: %v", err)
	}
	if !*dryRun {
		dir := filepath.Dir(config.FilePath())
		if id, err := config.MachineUUID(dir); err == nil {
			opts.Logf("machine id: %s (kept in %s)", id, filepath.Join(dir, "machine-id"))
		} else {
			opts.Logf("machine id unavailable: %v", err)
		}
	}
	present := install.AgentsPresent(opts.Home)
	for _, agent := range agents.orAll() {
		if len(agents) == 0 && !present[agent] {
			opts.Logf("%s: not used on this machine, skipped (rerun `agent-sessions install` after installing it)", agent)
			continue
		}
		if err := installers[agent](opts); err != nil {
			failed = true
			opts.Logf("%s: not changed: %v", agent, err)
		}
	}
	o.printf("agent-sessions command: %s\n", strings.Join(opts.Command, " "))
	for _, line := range opts.Log {
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

func runUninstall(args []string, o IO) (int, error) {
	var agents agentList
	fs := newFlags("uninstall", o)
	fs.Var(&agents, "agent", "only this agent: claude, codex or pi (repeatable; default all)")
	dryRun := fs.Bool("dry-run", false, "show what would change")
	if err := parse(fs, args); err != nil {
		return exitUsage, err
	}
	opts := &install.Options{Command: install.Command(), Home: config.Home(), DryRun: *dryRun, Remove: true}
	failed := false
	for _, agent := range agents.orAll() {
		if err := installers[agent](opts); err != nil {
			failed = true
			opts.Logf("%s: not changed: %v", agent, err)
		}
	}
	for _, line := range opts.Log {
		o.printf("  %s\n", line)
	}
	if failed {
		return exitFailed, nil
	}
	return exitOK, nil
}

func lockSync(ctx context.Context, cfg *config.Config, timeout time.Duration) (func(), error) {
	dir, err := syncer.LockDir(cfg)
	if err != nil {
		return nil, err
	}
	return state.Lock(ctx, dir, timeout)
}
