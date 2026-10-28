package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

type runtimeFlags struct {
	config, agent, session, source, path, role, action, platform, executable string
	nonce, workID, workAfter, sessionAfter                                   string
	apply, acknowledge                                                       bool
	id, after                                                                int64
	limit                                                                    int
}

func runtimeCommand(s string) bool {
	switch s {
	case "current-heads", "publication-status", "run", "hint", "hook", "hooks", "work-status", "quarantine", "replay", "resume", "service", "policy-pauses", "enrichment-status", "enrichment-replay", "optional-status", "optional-replay", "optional-quota-replay":
		return true
	}
	return false
}

func parseRuntimeFlags(cmd string, args []string, o IO) (runtimeFlags, error) {
	var f runtimeFlags
	fs := newFlags("capture-v1 "+cmd, o)
	fs.StringVar(&f.config, "config", "", "explicit private capture-runtime/v1 JSON (experimental; local or R2 backend)")
	switch cmd {
	case "hint":
		fs.StringVar(&f.agent, "agent", "", "claude, codex or pi")
		fs.StringVar(&f.session, "session", "", "root-qualified session identity")
		fs.StringVar(&f.source, "source", "", "stable source identity")
		fs.StringVar(&f.path, "path", "", "absolute source path")
		fs.StringVar(&f.role, "role", "main", "raw main or subagent transcript")
	case "hook":
		fs.StringVar(&f.agent, "agent", "", "claude, codex or pi")
		fs.StringVar(&f.path, "path", "", "source path; otherwise bounded native hook input on stdin")
	case "publication-status", "current-heads":
		fs.StringVar(&f.sessionAfter, "after-session", "", "next_cursor from the previous page; restart empty for each full sweep")
		fs.IntVar(&f.limit, "limit", 256, "page size, 1..256")
	case "work-status", "quarantine":
		fs.Int64Var(&f.after, "after", 0, "pagination coordinate")
		fs.IntVar(&f.limit, "limit", 64, "page size, 1..256")
	case "replay":
		fs.Int64Var(&f.id, "id", 0, "quarantine coordinate; stop coordinator first")
	case "enrichment-status", "optional-status":
		fs.StringVar(&f.workAfter, "after-id", "", "lexical enrichment work cursor")
		fs.IntVar(&f.limit, "limit", 64, "page size, 1..256")
	case "enrichment-replay", "optional-replay":
		fs.StringVar(&f.workID, "work-id", "", "work coordinate; stop coordinator first")
	case "optional-quota-replay":
		fs.StringVar(&f.agent, "agent", "", "agent owning the retained optional quota gap")
		fs.StringVar(&f.session, "session", "", "exact root-qualified session identity; stop coordinator first")
	case "policy-pauses":
		fs.StringVar(&f.action, "action", "list", "list or clear unresolved native opt-out barriers")
		fs.StringVar(&f.agent, "agent", "", "agent to reconcile")
		fs.StringVar(&f.nonce, "nonce", "", "exact inspected barrier nonce")
		fs.BoolVar(&f.apply, "apply", false, "explicitly clear only the inspected policy barrier")
		fs.BoolVar(&f.acknowledge, "acknowledge-unresolved-opt-outs", false, "affirm every unresolved opt-out has been reconciled")
	case "service", "hooks":
		fs.StringVar(&f.action, "action", "plan", "plan, install or uninstall; service also supports start/stop/restart")
		if cmd == "service" {
			fs.StringVar(&f.platform, "platform", runtime.GOOS, "darwin or linux")
		}
		fs.StringVar(&f.executable, "executable", "", "explicit absolute candidate binary")
		fs.BoolVar(&f.apply, "apply", false, "explicitly permit service filesystem and supervisor changes")
	}
	if err := parse(fs, args); err != nil {
		return f, err
	}
	if err := validateRuntimeFlags(cmd, f, fs.NArg()); err != nil {
		return f, err
	}
	return f, nil
}

func runCaptureRuntime(ctx context.Context, cmd string, args []string, o IO) (code int, err error) {
	f, err := parseRuntimeFlags(cmd, args, o)
	if err != nil {
		return exitUsage, err
	}
	c, err := capturedaemon.Load(f.config)
	if err != nil {
		return exitFailed, err
	}
	switch cmd {
	case "service":
		return runCaptureService(ctx, c, f, o)
	case "hooks":
		return runCaptureHooks(ctx, c, f, o)
	case "hook":
		return runNativeCaptureHook(ctx, c, f, os.Stdin)
	case "policy-pauses":
		return runCapturePolicyPauses(ctx, c, f, o)
	case "current-heads":
		return readCaptureCurrentHeads(ctx, c, f, o)
	case "publication-status":
		return readCapturePublicationHeads(ctx, c, f, o)
	case "enrichment-status":
		return readCaptureEnrichment(ctx, c, f, o)
	case "optional-status":
		return readCaptureOptional(ctx, c, f, o)
	case "hint":
		return enqueueCaptureHint(ctx, c, f)
	case "work-status", "quarantine":
		// Inspection must not take the active coordinator's lifetime flock.
		return readCaptureRuntime(ctx, cmd, c, f, o)
	case "run", "replay", "resume", "enrichment-replay", "optional-replay", "optional-quota-replay":
		return runCaptureWritable(ctx, cmd, c, f)
	default:
		return exitUsage, errors.New("unsupported capture runtime command")
	}
}

// All writable operations share exclusive queue ownership. Inspection and hooks
// are routed before here and cannot accidentally wait on that lifetime lock.
func runCaptureWritable(ctx context.Context, cmd string, c capturedaemon.Config, f runtimeFlags) (code int, err error) {
	var backend publication.Backend
	if cmd == "run" {
		backend, err = captureRuntimeBackend(ctx, c)
		if err != nil {
			if sessioncapture.IsCallerCancellation(ctx, err) {
				return exitOK, nil
			}
			return exitFailed, err
		}
	}
	r, err := newRuntimeComposition(c)
	if err != nil {
		return exitFailed, err
	}
	queueOptions := r.queueOptions()
	q, err := sessioncapture.Open(ctx, c.Spool, queueOptions)
	if err != nil {
		if cmd == "run" && sessioncapture.IsCallerCancellation(ctx, err) {
			return exitOK, nil
		}
		if errors.Is(err, sessioncapture.ErrQueueOpen) {
			return exitFailed, fmt.Errorf("capture coordinator unavailable; replay/resume require stopped coordinator: %w", err)
		}
		return exitFailed, fmt.Errorf("capture coordinator unavailable: %w", err)
	}
	defer func() { err = errors.Join(err, q.Close()) }()
	switch cmd {
	case "resume":
		err = q.Resume(ctx)
	case "replay":
		err = q.Replay(ctx, f.id)
	case "enrichment-replay":
		err = q.ReplayEnrichment(ctx, f.workID)
	case "optional-replay":
		err = q.ReplayOptionalRepair(ctx, f.workID)
	case "optional-quota-replay":
		err = q.ReplayOptionalRepairQuota(ctx, publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: f.agent, SessionID: f.session})
	case "run":
		discover, closeDiscovery := r.discovery(f.config)
		defer func() { err = errors.Join(err, closeDiscovery()) }()
		opts := sessioncapture.RuntimeOptions{DiscoverBatch: discover, PollInterval: time.Duration(c.PollSeconds) * time.Second, OperationTimeout: time.Duration(c.OperationSeconds) * time.Second, CaptureWorkers: c.CaptureWorkers, DeliveryWorkers: c.DeliveryWorkers, MaxAttempts: c.MaxAttempts}
		if c.Enrichment {
			opts.Enrich, opts.CheckEnrichmentAsset = r.enrichment(), r.enrichmentAssetPolicy()
		}
		err = q.Run(ctx, backend, opts)
	}
	return exitOK, err
}

func enqueueCaptureHint(ctx context.Context, c capturedaemon.Config, f runtimeFlags) (int, error) {
	req := sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: f.agent, SessionID: f.session}, SourceID: f.source, Path: f.path, Role: f.role}
	if !captureIdentityComplete(req.Identity) || f.source == "" || !filepath.IsAbs(f.path) || (f.role != "main" && f.role != "subagent") {
		return exitUsage, errors.New("hint requires explicit --agent --session --source --path and main/subagent --role")
	}
	r, err := newRuntimeComposition(c)
	if err != nil {
		return exitFailed, err
	}
	if err := r.policy(false)(ctx, req); err != nil {
		return exitFailed, err
	}
	return exitOK, sessioncapture.WriteHint(ctx, c.Spool, req)
}

func (r runtimeComposition) discovery(configPath string) (func(context.Context) (sessioncapture.DiscoveryBatch, error), func() error) {
	var cursor *capturesource.Cursor
	opts := r.sourceOptions(configPath)
	return func(ctx context.Context) (sessioncapture.DiscoveryBatch, error) {
			opts.Cursor = cursor
			result, err := capturesource.Discover(ctx, opts)
			cursor = result.Cursor
			batch := sessioncapture.DiscoveryBatch{Requests: result.Requests, Complete: true}
			for _, coverage := range result.Coverage {
				batch.Coverage = append(batch.Coverage, sessioncapture.DiscoveryScope{Agent: coverage.Root.Agent, Root: coverage.Root.Path, State: coverage.State, Examined: coverage.Examined, Enrichment: coverage.Enrichment})
				if coverage.State == "bounded" {
					batch.Complete = false
				}
			}
			for _, issue := range result.Issues {
				batch.Issues = append(batch.Issues, sessioncapture.DiscoveryIssue{RootAgent: issue.Root.Agent, RootPath: issue.Root.Path, Path: issue.Path, Code: issue.Code})
			}
			if batch.Complete && cursor != nil {
				err = errors.Join(err, cursor.Close())
				cursor = nil
			}
			// A partial inventory is durable coverage, not a failed callback. Only
			// actual cancellation/options/close errors inhibit sweep resolution.
			return batch, err
		}, func() error {
			if cursor != nil {
				return cursor.Close()
			}
			return nil
		}
}

func runCaptureService(ctx context.Context, c capturedaemon.Config, f runtimeFlags, o IO) (int, error) {
	if f.action != "plan" && f.action != "install" && f.action != "uninstall" && f.action != "restart" && f.action != "start" && f.action != "stop" {
		return exitUsage, errors.New("service action must be plan, install, uninstall, start, stop or restart")
	}
	if f.apply && f.action == "plan" {
		return exitUsage, errors.New("service plan does not accept --apply")
	}
	if f.apply && f.platform != runtime.GOOS {
		return exitUsage, errors.New("cannot apply a service for another platform")
	}
	plan, err := capturedaemon.ServicePlan(f.platform, c.Home, f.executable, f.config, os.Getuid())
	if err != nil {
		return exitUsage, err
	}
	if f.action != "plan" {
		err = capturedaemon.Apply(ctx, plan, f.action, f.apply, func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		})
		if err != nil {
			return exitFailed, err
		}
	}
	return exitOK, json.NewEncoder(o.Stdout).Encode(plan)
}
