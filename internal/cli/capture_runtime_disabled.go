package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Disable is durable session policy, not merely a property of this process.
// Resolve metadata only; never open the queue, emit hints, or read source bodies.
func runDisabledNativeCaptureHook(ctx context.Context, c capturedaemon.Config, f runtimeFlags, input io.Reader) (int, error) {
	unresolved := func() (int, error) { return unresolvedDisabledNativeHook(ctx, c, f.agent) }
	if f.agent != "claude" && f.agent != "codex" && f.agent != "pi" {
		return unresolved()
	}
	paths := []string{f.path}
	if f.path == "" {
		var err error
		paths, err = nativeCapturePaths(ctx, input)
		if err != nil {
			return unresolved()
		}
	}
	if err := disabledNativePolicyAvailable(c); err != nil {
		return unresolved()
	}
	for _, path := range paths {
		if ctx.Err() != nil || !filepath.IsAbs(path) || len(path) > 64<<10 {
			return unresolved()
		}
		path = filepath.Clean(path)
		req := sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: f.agent}, Path: path, Role: "main"}
		sf, err := runtimePrivacyOwner(ctx, c, req, false)
		if err != nil || sf == nil || sf.SessionKey == "" {
			return unresolved()
		}
		if err := runtimeFilesystemPolicy(c, path, false); err != nil {
			return exitFailed, errors.New("disabled hook source policy unavailable")
		}
		if _, err := exclusion.RecordFile(c.PolicyDir, *sf); err != nil {
			return exitFailed, errors.New("disabled hook session exclusion could not be persisted")
		}
	}
	return exitOK, nil
}

// A damaged or absent registry must not be replaced with an apparently fresh one.
func disabledNativePolicyAvailable(c capturedaemon.Config) error {
	for _, dir := range []string{c.PolicyDir, filepath.Join(c.PolicyDir, "exclusions")} {
		if err := capturedaemon.SafePath(dir); err != nil {
			return errors.New("effective exclusions unavailable")
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return errors.New("effective exclusions unavailable")
		}
	}
	_, err := exclusion.Load(c.PolicyDir)
	return err
}
