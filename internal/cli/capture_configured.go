package cli

import (
	"context"
	"errors"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
)

func runConfiguredCapture(ctx context.Context, cmd string, flags captureFlags) (int, error) {
	if cmd != "init" && cmd != "export" {
		return exitUsage, errors.New("configured backend requires init/export; use runtime commands for queue work")
	}
	if flags.store != "" || flags.spool != "" || flags.policyDir != "" || flags.source != "" || flags.path != "" {
		return exitUsage, errors.New("configured backend cannot be combined with local storage/work flags")
	}
	cfg, err := capturedaemon.Load(flags.runtimeConfig)
	if err != nil {
		return exitFailed, err
	}
	if flags.archive != "" && flags.archive != cfg.ArchiveID || flags.machine != "" && flags.machine != cfg.MachineID {
		return exitUsage, errors.New("configured publication identity mismatch")
	}
	flags.archive, flags.machine = cfg.ArchiveID, cfg.MachineID
	if cmd == "export" {
		if flags.output == "" || !captureIdentityComplete(flags.identity()) {
			return exitUsage, errors.New("configured export requires --output --agent --session")
		}
		if err := capturebackend.ValidateIdentity(cfg.BackendSpec(), flags.identity()); err != nil {
			return exitUsage, err
		}
	}
	backend, err := captureBackend(ctx, cfg, false)
	if err != nil {
		return exitFailed, err
	}
	if cmd == "init" {
		return exitOK, publication.WriteArchive(ctx, backend, publication.Archive{Contract: publication.Contract, ArchiveID: cfg.ArchiveID})
	}
	_, err = publication.ExportSessionBounded(ctx, backend, flags.identity(), flags.output, flags.maxBytes)
	return exitOK, err
}
