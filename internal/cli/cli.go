// Package cli is the agent-sessions command line.
package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/doctor"
	"github.com/mich-murphy/memory/internal/hook"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/store"
)

const usage = `agent-sessions: collect coding-agent sessions into one bucket.

Usage:
  agent-sessions install      install hooks and the Pi extension
  agent-sessions backfill     upload every session already on this machine
  agent-sessions sync         upload new or changed session files
  agent-sessions doctor       check settings, bucket access and hooks
  agent-sessions status       what has been synced from this machine
  agent-sessions cat KEY      print a raw file rebuilt from the bucket (base object plus segments)
  agent-sessions uninstall    remove hooks and the Pi extension
  agent-sessions --version

Run agent-sessions COMMAND -h for a command's options.
`

const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2
	exitLocked = 3
)

// errUsage is returned once the flag package has already printed what was wrong.
var errUsage = errors.New("usage")

type IO struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (o IO) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(o.Stdout, format, args...) // nowhere to report a broken stdout
}

func (o IO) errorf(format string, args ...any) {
	_, _ = fmt.Fprintf(o.Stderr, format, args...) // nowhere to report a broken stderr
}

type agentList []string

func (a *agentList) String() string { return strings.Join(*a, ",") }

func (a *agentList) Set(v string) error {
	if !slices.Contains(adapters.Names, v) {
		return fmt.Errorf("%w: unknown agent %q (choose from %s)", errUsage, v, strings.Join(adapters.Names, ", "))
	}
	*a = append(*a, v)
	return nil
}

func (a *agentList) orAll() []string {
	if len(*a) == 0 {
		return slices.Clone(adapters.Names)
	}
	return *a
}

func newFlags(name string, o IO) *flag.FlagSet {
	fs := flag.NewFlagSet("agent-sessions "+name, flag.ContinueOnError)
	fs.SetOutput(o.Stderr)
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		return flag.ErrHelp
	default:
		return errUsage
	}
}

func Main(ctx context.Context, args []string, version string, o IO) int {
	if len(args) == 0 {
		o.errorf("%s", usage)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "hook":
		runHook(ctx, rest)
		return exitOK
	case "--version", "-version", "version":
		o.printf("%s\n", version)
		return exitOK
	case "-h", "--help", "help":
		o.printf("%s", usage)
		return exitOK
	}
	var code int
	var err error
	switch cmd {
	case "sync", "backfill":
		code, err = runSync(ctx, cmd, rest, version, o)
	case "install":
		code, err = runInstall(ctx, rest, o)
	case "uninstall":
		code, err = runUninstall(ctx, rest, o)
	case "doctor":
		code, err = runDoctor(ctx, rest, o)
	case "status":
		code, err = runStatus(ctx, rest, o)
	case "cat":
		code, err = runCat(ctx, rest, o)
	default:
		o.errorf("agent-sessions: unknown command %q\n\n%s", cmd, usage)
		return exitUsage
	}
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, errUsage):
		return exitUsage
	case err != nil:
		o.errorf("agent-sessions: %v\n", err)
		return max(code, exitFailed)
	}
	return code
}

// runHook parses a hook's arguments by hand, so a misconfigured hook still never fails the
// agent.
func runHook(ctx context.Context, args []string) {
	if len(args) == 0 || !slices.Contains(adapters.Names, args[0]) {
		return
	}
	agent := args[0]
	var paths []string
	var event string
	var extra hook.ExtensionArgs
	for i := 1; i+1 < len(args); i++ {
		v := args[i+1]
		switch args[i] {
		case "--path":
			paths = append(paths, v)
		case "--event":
			event = cmp.Or(event, v)
		case "--source":
			extra.Source = cmp.Or(extra.Source, v)
		case "--cwd":
			extra.Cwd = cmp.Or(extra.Cwd, v)
		case "--argv":
			extra.Argv = cmp.Or(extra.Argv, v)
		default:
			continue
		}
		i++
	}
	hook.Run(ctx, agent, paths, event, extra)
}

func runDoctor(ctx context.Context, args []string, o IO) (int, error) {
	if err := parse(newFlags("doctor", o), args); err != nil {
		return exitUsage, err
	}
	cfg, err := config.Load(config.FilePath())
	if err != nil {
		return exitFailed, err
	}
	if doctor.Run(ctx, cfg, o.Stdout) {
		return exitOK, nil
	}
	return exitFailed, nil
}

func runCat(ctx context.Context, args []string, o IO) (int, error) {
	fs := newFlags("cat", o)
	fs.Usage = func() {
		o.errorf("usage: agent-sessions cat raw/agent=<agent>/machine=<machine>/<key>.zst | blobs/<sha256>.zst\n")
	}
	if err := parse(fs, args); err != nil {
		return exitUsage, err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage, errUsage
	}
	key := fs.Arg(0)
	cfg, err := config.Load(config.FilePath())
	if err != nil {
		return exitFailed, err
	}
	st, err := store.Open(cfg)
	if err != nil {
		return exitFailed, err
	}
	var data []byte
	if strings.HasPrefix(key, "raw/") {
		data, err = raw.ReadKey(ctx, st, key)
	} else {
		var body []byte
		if body, err = st.Get(ctx, key); err == nil {
			data, err = raw.Decompress(body)
		}
	}
	if err != nil {
		return exitFailed, err
	}
	if _, err := o.Stdout.Write(data); err != nil {
		return exitFailed, fmt.Errorf("write: %w", err)
	}
	return exitOK, nil
}
