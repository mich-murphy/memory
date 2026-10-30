package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func runtimeExternalScope(top string) bool {
	switch top {
	case "file-history", "session-env", "debug", "todos", "tasks":
		return true
	default:
		return false
	}
}

func runtimeExternalPrivacyOwner(ctx context.Context, c capturedaemon.Config, req sessioncapture.Request, root, rel string, discovery bool) (*adapters.SessionFile, error) {
	// Directory traversal was handled before this call. An unresolved leaf may be
	// inspected as metadata only: the source classifier must independently resolve
	// its owner before emitting work, and final capture/delivery always require it.
	unavailable := func() (*adapters.SessionFile, error) {
		if discovery && req.Role == "" {
			return nil, nil
		}
		return nil, errors.New("source ownership unavailable or ambiguous")
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || len(parts[1]) < 36 {
		return unavailable()
	}
	uuid := sessioncapture.OwnerUUID(parts[1][:36] + ".jsonl")
	if uuid == "" {
		return unavailable()
	}
	session, legacy, err := sessioncapture.ReadOwnership(ctx, c.Spool, c.ArchiveID, req.Identity.Agent, root, uuid)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return unavailable()
	}
	a := adapters.All(c.Home)[req.Identity.Agent]
	if _, ok := capturesource.OwnerMainPath(a, capturesource.Root{Agent: req.Identity.Agent, Path: root}, uuid, session, legacy); !ok {
		return unavailable()
	}
	// Registry.Excludes compares this independently derived root/session and the
	// artifact path. A supplied request identity or association cannot override it.
	sf := adapters.SessionFile{Agent: req.Identity.Agent, Root: root, Path: req.Path, SessionKey: legacy}
	return &sf, nil
}
