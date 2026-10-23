package cli

import (
	"context"
	"encoding/json"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func readCaptureRuntime(ctx context.Context, cmd string, c capturedaemon.Config, f runtimeFlags, o IO) (int, error) {
	view, err := sessioncapture.ReadRuntime(ctx, c.Spool, c.ArchiveID, f.after, f.limit)
	if err != nil {
		return exitFailed, err
	}
	if cmd == "quarantine" {
		return exitOK, json.NewEncoder(o.Stdout).Encode(view.Quarantine)
	}
	return exitOK, json.NewEncoder(o.Stdout).Encode(view)
}

func readCaptureOptional(ctx context.Context, c capturedaemon.Config, f runtimeFlags, o IO) (int, error) {
	rows, err := sessioncapture.ReadOptionalRepairWorkStatus(ctx, c.Spool, c.ArchiveID, f.workAfter, f.limit)
	if err != nil {
		return exitFailed, err
	}
	return exitOK, json.NewEncoder(o.Stdout).Encode(rows)
}

func readCaptureEnrichment(ctx context.Context, c capturedaemon.Config, f runtimeFlags, o IO) (int, error) {
	rows, err := sessioncapture.ReadEnrichmentWorkStatus(ctx, c.Spool, c.ArchiveID, f.workAfter, f.limit)
	if err != nil {
		return exitFailed, err
	}
	return exitOK, json.NewEncoder(o.Stdout).Encode(rows)
}
