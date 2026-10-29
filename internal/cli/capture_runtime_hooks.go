package cli

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturehooks"
)

func runCaptureHooks(ctx context.Context, c capturedaemon.Config, f runtimeFlags, o IO) (int, error) {
	if f.action != "plan" && f.action != "install" && f.action != "uninstall" {
		return exitUsage, errors.New("hooks action must be plan, install or uninstall")
	}
	if f.apply && f.action == "plan" {
		return exitUsage, errors.New("hooks plan does not accept --apply")
	}
	roots, err := c.HookRoots()
	if err != nil {
		return exitFailed, err
	}
	if len(roots) == 0 {
		return exitUsage, errors.New("hooks require a nonempty configured source inventory")
	}
	plan, err := capturehooks.Build(capturehooks.Options{Home: c.Home, Roots: roots, Executable: f.executable, ConfigPath: f.config, Remove: f.action == "uninstall"})
	if err != nil {
		return exitFailed, err
	}
	if err := capturehooks.Apply(ctx, plan, f.apply); err != nil {
		return exitFailed, err
	}
	return exitOK, json.NewEncoder(o.Stdout).Encode(plan)
}
