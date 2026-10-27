package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

var errNativeCapturePathless = errors.New("native hook has no source path")

// This opt-in native hook entry point does not implicitly change legacy hooks.
// It owns discovery hints and, when enabled, immutable native evidence.
// Periodic discovery can recover missed hints while eligible sources remain;
// it cannot recover vanished bytes or missed native launch context.
// Source/session identities use the same classifier as discovery.
func runNativeCaptureHook(ctx context.Context, c capturedaemon.Config, f runtimeFlags, input io.Reader) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if os.Getenv("AGENT_SESSIONS_DISABLE") != "" {
		return runDisabledNativeCaptureHook(ctx, c, f, input)
	}
	if f.agent != "claude" && f.agent != "codex" && f.agent != "pi" {
		return exitUsage, errors.New("hook requires an explicit supported agent")
	}
	var payload map[string]json.RawMessage
	paths := []string{f.path}
	if f.path == "" {
		var err error
		payload, paths, err = nativeCapturePayload(ctx, input)
		if errors.Is(err, errNativeCapturePathless) {
			return exitOK, sessioncapture.WriteDiscoveryHint(ctx, c.Spool, c.ArchiveID, c.MachineID, f.agent)
		}
		if err != nil {
			return exitFailed, err
		}
	}
	policy := runtimePolicy(c)
	opts, err := runtimeSourceOptions(c, c.ConfigPath())
	if err != nil {
		return exitFailed, err
	}
	invocationID := nativeInvocationID()
	for i, path := range paths {
		classify := capturesource.Classify
		if c.Enrichment {
			classify = capturesource.ClassifyNative
		}
		req, err := classify(ctx, opts, f.agent, path)
		if err != nil {
			return exitFailed, errors.New("hook source unavailable, unsupported or excluded")
		}
		if err := policy(ctx, req); err != nil {
			return exitFailed, err
		}
		if err := maybeWriteNativeLaunch(ctx, c.Enrichment, c.Spool, req, payload, invocationID+"-"+strconv.Itoa(i)); err != nil {
			return exitFailed, err
		}
		if err := sessioncapture.WriteHint(ctx, c.Spool, req); err != nil {
			return exitFailed, err
		}
	}
	return exitOK, nil
}

// The transient hook process exits after this bounded wait. A reader blocked in
// the kernel may remain blocked until that exit; this helper is never used by the
// long-lived coordinator. Tests close owned pipe readers when testing timeout.
func nativeCapturePaths(ctx context.Context, input io.Reader) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type result struct {
		paths []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		paths, err := decodeNativeCapturePaths(input)
		done <- result{paths: paths, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, errors.New("native hook input timed out")
	case r := <-done:
		return r.paths, r.err
	}
}

func decodeNativeCapturePaths(input io.Reader) ([]string, error) {
	_, paths, err := decodeNativeCapturePayload(input)
	return paths, err
}

func decodeNativeCapturePayload(input io.Reader) (map[string]json.RawMessage, []string, error) {
	body, err := io.ReadAll(io.LimitReader(input, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return nil, nil, errors.New("invalid or oversized native hook input")
	}
	var payload map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if dec.Decode(&payload) != nil || payload == nil {
		return nil, nil, errors.New("invalid native hook input")
	}
	var trailing any
	if !errors.Is(dec.Decode(&trailing), io.EOF) {
		return nil, nil, errors.New("invalid native hook input")
	}
	var paths []string
	seen := map[string]bool{}
	for _, key := range []string{"transcript_path", "agent_transcript_path", "session_path", "session_file", "rollout_path"} {
		var path string
		if value, ok := payload[key]; ok {
			if err := json.Unmarshal(value, &path); err != nil {
				return nil, nil, errors.New("invalid native hook path")
			}
			if path != "" && !seen[path] {
				paths = append(paths, path)
				seen[path] = true
			}
		}
	}
	if len(paths) == 0 {
		return payload, nil, errNativeCapturePathless
	}
	return payload, paths, nil
}
