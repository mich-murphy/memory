package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/hook"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func nativeCapturePayload(ctx context.Context, input io.Reader) (map[string]json.RawMessage, []string, error) {
	type result struct {
		payload map[string]json.RawMessage
		paths   []string
		err     error
	}
	done := make(chan result, 1)
	go func() { p, paths, err := decodeNativeCapturePayload(input); done <- result{p, paths, err} }()
	select {
	case <-ctx.Done():
		return nil, nil, errors.New("native hook input timed out")
	case r := <-done:
		return r.payload, r.paths, r.err
	}
}

var nativeMetadataID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$`)

func nativeString(payload map[string]json.RawMessage, key string, path bool) string {
	var value string
	if json.Unmarshal(payload[key], &value) != nil {
		return ""
	}
	if path {
		if len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n") {
			return value
		}
		return ""
	}
	if nativeMetadataID.MatchString(value) {
		return value
	}
	return ""
}

func writeNativeLaunch(ctx context.Context, spool string, req sessioncapture.Request, payload map[string]json.RawMessage, invocationID string) error {
	event := nativeString(payload, "hook_event_name", false)
	if event == "" {
		event = nativeString(payload, "event", false)
	}
	if !slices.Contains([]string{"SessionStart", "session_start", "session_resume", "SessionResume"}, event) {
		return nil
	}
	c := sessioncapture.NativeContext{Event: event, ContextOrigin: "native-hook", Missing: []string{"native_event_id"}}
	c.Source = nativeString(payload, "source", false)
	if !slices.Contains([]string{"startup", "resume", "clear", "compact", "reload", "new", "fork"}, c.Source) {
		c.Source = ""
	}
	c.CWD = nativeString(payload, "cwd", true)
	c.NativeSessionID = nativeString(payload, "session_id", false)
	c.Model = nativeString(payload, "model", false)
	c.Provider = nativeString(payload, "provider", false)
	c.AgentVersion = nativeString(payload, "agent_version", false)
	c.PermissionMode = nativeString(payload, "permission_mode", false)
	if !slices.Contains([]string{"default", "acceptEdits", "bypassPermissions", "plan", "dontAsk", "auto", "read-only", "workspace-write", "danger-full-access"}, c.PermissionMode) {
		c.PermissionMode = ""
	}
	var argv []string
	extension := req.Identity.Agent == "pi" && nativeString(payload, "context_origin", false) == "extension"
	if extension {
		c.ContextOrigin = "extension"
		if json.Unmarshal(payload["argv"], &argv) == nil {
			c.Argv = launch.SanitizeNativeArgs(argv)
		}
	} else {
		c.Argv, _ = hook.InspectNativeArgs(ctx, req.Identity.Agent)
	}
	// Environment belongs to this transient hook, never the coordinator. Pi's
	// extension inheritance has the same native process environment boundary.
	c.Environment = launch.SanitizeNativeEnvironment(os.Environ())
	for _, field := range []struct{ name, value string }{{"cwd", c.CWD}, {"native_session_id", c.NativeSessionID}, {"model", c.Model}, {"provider", c.Provider}, {"permission_mode", c.PermissionMode}, {"agent_version", c.AgentVersion}} {
		if field.value == "" {
			c.Missing = append(c.Missing, field.name)
		}
	}
	if len(c.Argv) == 0 {
		c.Missing = append(c.Missing, "agent_argv")
	}
	record := sessioncapture.NativeEvidence{Contract: sessioncapture.EnrichmentContract, ID: invocationID, Identity: req.Identity, Request: req, ObservedAtNS: time.Now().UnixNano(), Context: c}
	return sessioncapture.WriteNativeEvidence(ctx, spool, record)
}

func nativeInvocationID() string { return rand.Text() }

func maybeWriteNativeLaunch(ctx context.Context, enabled bool, spool string, req sessioncapture.Request, payload map[string]json.RawMessage, id string) error {
	if !enabled {
		return nil
	}
	return writeNativeLaunch(ctx, spool, req, payload, id)
}
