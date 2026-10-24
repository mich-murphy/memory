package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

type captureFlags struct {
	store, spool, archive, machine, agent, session       string
	source, path, role, output, policyDir, runtimeConfig string
	quota, maxBytes                                      int64
}

func (c captureFlags) identity() publication.Identity {
	return publication.Identity{ArchiveID: c.archive, MachineID: c.machine, Agent: c.agent, SessionID: c.session}
}

func parseCaptureFlags(cmd string, args []string, o IO) (captureFlags, error) {
	var c captureFlags
	fs := newFlags("capture-v1 "+cmd, o)
	fs.StringVar(&c.store, "store", "", "explicit isolated local object store")
	fs.StringVar(&c.runtimeConfig, "config", "", "explicit private runtime configuration for init/export")
	fs.StringVar(&c.spool, "spool", "", "private durable work directory, not legacy staging")
	fs.StringVar(&c.archive, "archive", "", "explicit archive instance identity")
	fs.StringVar(&c.machine, "machine", "", "existing machine identity; never generated")
	fs.StringVar(&c.agent, "agent", "", "claude, codex, or pi")
	fs.StringVar(&c.session, "session", "", "root-qualified session identity")
	fs.StringVar(&c.source, "source", "", "stable source identity within this session")
	fs.StringVar(&c.path, "path", "", "source transcript path")
	fs.StringVar(&c.role, "role", "main", "main or subagent transcript")
	fs.StringVar(&c.output, "output", "", "new private export directory")
	fs.StringVar(&c.policyDir, "policy-dir", "", "preserved effective exclusion registry directory; required for capture/delivery")
	fs.Int64Var(&c.maxBytes, "max-bytes", publication.DefaultExportMaxBytes, "maximum total decoded export bytes, 1..1099511627776 (default 1 GiB)")
	fs.Int64Var(&c.quota, "max-spool-bytes", 1<<30, "maximum retained encoded spool bytes; no eviction")
	if err := parse(fs, args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 || (c.archive == "" && c.runtimeConfig == "") {
		return c, fmt.Errorf("%w: capture-v1 requires --archive or --config and no positional arguments", errUsage)
	}
	if cmd == "export" && (c.maxBytes <= 0 || c.maxBytes > publication.MaxExportBytes) {
		return c, fmt.Errorf("%w: export --max-bytes must be 1..1099511627776", errUsage)
	}
	return c, nil
}

// runCapture exposes the replacement only through explicit experimental commands.
// Destination credentials are resolved only for configured backend operations;
// the legacy v3 namespace and installed runtime remain untouched.
func runCapture(ctx context.Context, args []string, o IO) (int, error) {
	if len(args) == 0 {
		return exitUsage, errors.New("capture-v1 requires init, capture, deliver, status, export, fixture, run, hint, hook, hooks, work-status, publication-status, current-heads, quarantine, replay, resume, enrichment-status, enrichment-replay, policy-pauses, or service")
	}
	if args[0] == "fixture" {
		return runCaptureFixture(ctx, args[1:], o)
	}
	if args[0] == "isolation-fixture" {
		return runCaptureIsolationFixture(ctx, args[1:], o)
	}
	if runtimeCommand(args[0]) {
		return runCaptureRuntime(ctx, args[0], args[1:], o)
	}
	c, err := parseCaptureFlags(args[0], args[1:], o)
	if err != nil {
		return exitUsage, err
	}
	if c.runtimeConfig != "" {
		return runConfiguredCapture(ctx, args[0], c)
	}
	backend := publication.NewLocal(c.store)
	switch args[0] {
	case "init":
		if c.store == "" {
			return exitUsage, errors.New("init requires --store")
		}
		return exitOK, publication.WriteArchive(ctx, backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.archive})
	case "export":
		if c.store == "" || c.output == "" || !captureIdentityComplete(c.identity()) {
			return exitUsage, errors.New("export requires --store, --output, --machine, --agent, --session")
		}
		_, err := publication.ExportSessionBounded(ctx, backend, c.identity(), c.output, c.maxBytes)
		return exitOK, err
	case "capture", "deliver", "status":
		return runCaptureWork(ctx, args[0], c, o)
	default:
		return exitUsage, errors.New("unknown capture-v1 command")
	}
}

func runCaptureWork(ctx context.Context, cmd string, c captureFlags, o IO) (code int, err error) {
	if c.spool == "" {
		return exitUsage, errors.New("work commands require --spool")
	}
	if cmd != "status" && (!captureIdentityComplete(c.identity()) || c.policyDir == "") {
		return exitUsage, errors.New("capture/deliver require --machine, --agent, --session, and explicit --policy-dir")
	}
	queue, err := sessioncapture.Open(ctx, c.spool, sessioncapture.Options{ArchiveID: c.archive, MaxSpoolBytes: c.quota, CheckPolicy: capturePolicy(c.policyDir, c.spool)})
	if err != nil {
		return exitFailed, err
	}
	defer func() { err = errors.Join(err, queue.Close()) }()
	switch cmd {
	case "capture":
		if c.source == "" || c.path == "" || (c.role != "main" && c.role != "subagent") {
			return exitUsage, errors.New("manual capture requires --source, --path and main/subagent --role; runtime discovery/enrichment handles associated artifacts")
		}
		_, err = queue.Capture(ctx, sessioncapture.Request{Identity: c.identity(), SourceID: c.source, Role: c.role, Path: c.path})
	case "deliver":
		if c.store == "" {
			return exitUsage, errors.New("deliver requires --store")
		}
		err = queue.Deliver(ctx, publication.NewLocal(c.store), c.identity())
	case "status":
		status, statusErr := queue.Status(ctx)
		if statusErr != nil {
			return exitFailed, statusErr
		}
		err = json.NewEncoder(o.Stdout).Encode(status)
	}
	return exitOK, err
}

func captureIdentityComplete(id publication.Identity) bool {
	return id.MachineID != "" && id.SessionID != "" && (id.Agent == "claude" || id.Agent == "codex" || id.Agent == "pi")
}

// Every call reloads effective local exclusions, including at delivery. An unreadable
// registry fails closed. This local CLI does not recover remote policy and therefore
// is not an authorized production rebuild path.
func capturePolicy(policyDir, spool string) func(context.Context, sessioncapture.Request) error {
	return func(ctx context.Context, req sessioncapture.Request) error {
		return checkCapturePolicy(ctx, req, policyDir, spool)
	}
}

func checkCapturePolicy(ctx context.Context, req sessioncapture.Request, policyDir, spool string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if os.Getenv("AGENT_SESSIONS_DISABLE") != "" {
		return errors.New("capture disabled by effective environment policy")
	}
	if policyDir == "" {
		return errors.New("effective exclusion registry is required")
	}
	// exclusion.Load intentionally treats a missing registry as empty for legacy
	// callers. Durable delivery must distinguish initialized empty policy from
	// unavailable policy, including a registry removed after local acceptance.
	for _, dir := range []string{policyDir, filepath.Join(policyDir, "exclusions")} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return errors.New("effective exclusions unavailable")
		}
	}
	registry, err := exclusion.Load(policyDir)
	if err != nil {
		return errors.New("effective exclusions unavailable")
	}
	a := adapters.All(config.Home())[req.Identity.Agent]
	if a == nil {
		return errors.New("unsupported agent")
	}
	if registry.ExcludesPath(a, req.Path) || captureSessionExcluded(registry, req.Identity) {
		return errors.New("session excluded by effective policy")
	}
	resolved, resolveErr := capturepolicy.Resolve(req.Path)
	if resolveErr != nil && !errors.Is(resolveErr, os.ErrNotExist) {
		return errors.New("source policy resolution unavailable")
	}
	for _, state := range []string{config.StateDir(), policyDir, spool} {
		policy, err := capturepolicy.New(config.FilePath(), state)
		if err != nil || policy.Excludes(resolved.Identities) {
			return errors.New("source excluded by filesystem policy")
		}
	}
	return nil
}

func captureSessionExcluded(registry exclusion.Registry, id publication.Identity) bool {
	// Retain session identity even if a source has disappeared.
	for _, record := range registry {
		if record.Agent == id.Agent && record.SessionKey == id.SessionID {
			return true
		}
	}
	return false
}

func fixtureFlags(args []string, o IO) (string, string, error) {
	fs := newFlags("capture-v1 fixture", o)
	output := fs.String("output", "", "new fixture directory; synthetic sources only")
	backend := fs.String("backend", "local", "fixture backend: local or sdk-fixture (offline)")
	if err := parse(fs, args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 0 || *output == "" || (*backend != "local" && *backend != "sdk-fixture") {
		return "", "", fmt.Errorf("%w: fixture requires --output and a supported backend", errUsage)
	}
	root, err := filepath.Abs(*output)
	return root, *backend, err
}
