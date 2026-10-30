package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/mich-murphy/memory/internal/capturedaemon"
)

const unresolvedNativeVersion = "native-optout-barrier/v1"

type unresolvedNativeMarker struct {
	Version      string `json:"version"`
	Agent        string `json:"agent"`
	Nonce        string `json:"nonce"`
	Count        uint64 `json:"count"`
	ObservedAtNS int64  `json:"observed_at_ns"`
	Reason       string `json:"reason"`
}

func supportedNativeAgent(agent string) bool {
	return agent == "claude" || agent == "codex" || agent == "pi"
}

func unresolvedNativePath(c capturedaemon.Config, agent string) (string, error) {
	if !supportedNativeAgent(agent) {
		return "", errors.New("unsupported native policy agent")
	}
	path := filepath.Join(c.PolicyDir, "capture-unresolved", agent+".json")
	if err := capturedaemon.SafePath(path); err != nil {
		return "", errors.New("unresolved native policy unavailable")
	}
	return path, nil
}

func privateNativePolicyPath(path string, directory bool) error {
	if err := capturedaemon.SafePath(path); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 || st.IsDir() != directory || (!directory && !st.Mode().IsRegular()) {
		return errors.New("unsafe unresolved native policy")
	}
	return nil
}

func readUnresolvedNativeMarker(path, agent string) (unresolvedNativeMarker, error) {
	var m unresolvedNativeMarker
	if err := privateNativePolicyPath(filepath.Dir(path), true); err != nil {
		return m, err
	}
	if err := privateNativePolicyPath(path, false); err != nil {
		return m, err
	}
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	body, err := io.ReadAll(io.LimitReader(f, 4097))
	closeErr := f.Close()
	if err != nil || closeErr != nil || len(body) > 4096 {
		return m, errors.New("unresolved native policy unreadable")
	}
	if json.Unmarshal(body, &m) != nil || m.Version != unresolvedNativeVersion || m.Agent != agent || len(m.Nonce) != 26 || m.Count == 0 || m.ObservedAtNS <= 0 || m.Reason != "disabled_owner_unresolved" {
		return m, errors.New("unresolved native policy malformed")
	}
	return m, nil
}

// runtimeUnresolvedNativePolicy blocks only the agent whose disabled observation
// had no provable owner. It never treats an enabled event as barrier repair.
func runtimeUnresolvedNativePolicy(c capturedaemon.Config, agent string) error {
	path, err := unresolvedNativePath(c, agent)
	if err != nil {
		return err
	}
	_, err = readUnresolvedNativeMarker(path, agent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("unresolved native policy unavailable")
	}
	return errors.New("agent capture paused: unresolved disabled native session")
}

// recordUnresolvedNativePolicy owns an independent short lock, not Queue's lease.
// There are exactly three possible agent marker names; payload/path text is absent.
func recordUnresolvedNativePolicy(ctx context.Context, c capturedaemon.Config, agent string) error {
	path, err := unresolvedNativePath(c, agent)
	if err != nil {
		return err
	}
	if err := disabledNativePolicyAvailable(c); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := privateNativePolicyPath(dir, true); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, ".lock")
	if err := ensureNativePolicyLock(lockPath); err != nil {
		return err
	}
	lock := flock.New(lockPath)
	ok, err := lock.TryLockContext(ctx, 5*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("unresolved native policy lock unavailable")
	}
	defer func() { _ = lock.Unlock() }()
	old, err := readUnresolvedNativeMarker(path, agent)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	count := old.Count + 1
	if count == 0 {
		count = old.Count
	}
	m := unresolvedNativeMarker{unresolvedNativeVersion, agent, rand.Text(), count, time.Now().UnixNano(), "disabled_owner_unresolved"}
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := capturedaemon.WritePrivate(path, body); err != nil {
		return err
	}
	return syncNativePolicyDirectory(c.PolicyDir)
}

func ensureNativePolicyLock(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		if err := f.Close(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	return privateNativePolicyPath(path, false)
}

func syncNativePolicyDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}

func unresolvedDisabledNativeHook(ctx context.Context, c capturedaemon.Config, agent string) (int, error) {
	if supportedNativeAgent(agent) {
		if err := recordUnresolvedNativePolicy(ctx, c, agent); err != nil {
			return exitFailed, errors.New("disabled hook unresolved policy could not be persisted")
		}
	}
	return exitFailed, errors.New("disabled hook session exclusion unresolved")
}
