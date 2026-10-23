package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/mich-murphy/memory/internal/capturedaemon"
)

func nativePolicyPauses(c capturedaemon.Config) ([]unresolvedNativeMarker, error) {
	var out []unresolvedNativeMarker
	for _, agent := range []string{"claude", "codex", "pi"} {
		path, err := unresolvedNativePath(c, agent)
		if err != nil {
			return nil, err
		}
		marker, err := readUnresolvedNativeMarker(path, agent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.New("unresolved native policy unavailable")
		}
		out = append(out, marker)
	}
	return out, nil
}

// An operator must reconcile every unresolved opt-out before clearing this
// barrier. The nonce protects a newer disabled observation from stale resolution.
// Neither an enabled hook nor destination resume calls this function.
func clearUnresolvedNativePolicy(ctx context.Context, c capturedaemon.Config, agent, nonce string) (err error) {
	path, err := unresolvedNativePath(c, agent)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(filepath.Dir(path), ".lock")
	if err := privateNativePolicyPath(lockPath, false); err != nil {
		return errors.New("unresolved native policy lock unavailable")
	}
	lock := flock.New(lockPath)
	ok, err := lock.TryLockContext(ctx, 5*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("unresolved native policy lock unavailable")
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	marker, err := readUnresolvedNativeMarker(path, agent)
	if err != nil {
		return errors.New("unresolved native policy unavailable")
	}
	if nonce == "" || marker.Nonce != nonce {
		return errors.New("unresolved native policy changed; inspect again")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return errors.New("unresolved native policy clear failed")
	}
	return errors.Join(syncNativePolicyDirectory(filepath.Dir(path)), syncNativePolicyDirectory(c.PolicyDir))
}

func runCapturePolicyPauses(ctx context.Context, c capturedaemon.Config, f runtimeFlags, o IO) (int, error) {
	if f.action != "list" && f.action != "clear" {
		return exitUsage, errors.New("policy-pauses action must be list or clear")
	}
	if f.action == "clear" {
		if !f.apply || !f.acknowledge || !supportedNativeAgent(f.agent) || f.nonce == "" {
			return exitUsage, errors.New("clear requires --agent --nonce --apply --acknowledge-unresolved-opt-outs after policy reconciliation")
		}
		if err := clearUnresolvedNativePolicy(ctx, c, f.agent, f.nonce); err != nil {
			return exitFailed, err
		}
	} else if f.apply || f.acknowledge || f.nonce != "" || f.agent != "" {
		return exitUsage, errors.New("policy-pauses list is read-only")
	}
	markers, err := nativePolicyPauses(c)
	if err != nil {
		return exitFailed, err
	}
	return exitOK, json.NewEncoder(o.Stdout).Encode(markers)
}
