package capturesource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// ClassifyNative admits metadata before a main/subagent transcript exists. It
// shares ordinary policy/root admission; payload session IDs are never authority.
func ClassifyNative(ctx context.Context, opts Options, agent, path string) (sessioncapture.Request, error) {
	return classifyPath(ctx, opts, agent, path, true)
}

func classifyAbsentNative(ctx context.Context, opts Options, policy *capturepolicy.Policy, root Root, path string) (sessioncapture.Request, error) {
	fail := func(code string) (sessioncapture.Request, error) {
		return sessioncapture.Request{}, &ClassificationError{Issue{root, path, code}}
	}
	rel, _ := filepath.Rel(root.Path, path)
	current := root.Path
	// Check each existing ancestor without following aliases. Missing components
	// are permitted only after a real configured directory root was established.
	parts := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for i, part := range parts {
		if err := ctx.Err(); err != nil {
			return sessioncapture.Request{}, err
		}
		current = filepath.Join(current, part)
		if !allowed(ctx, opts, policy, root, current) {
			return fail("policy_denied")
		}
		st, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && i > 0 {
			continue
		}
		if err != nil {
			return fail("source_unreadable")
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fail("alias_integration_required")
		}
		if i < len(parts)-1 && !st.IsDir() {
			return fail("nonregular_source")
		}
		if i == len(parts)-1 {
			return fail("source_unreadable")
		}
	}
	sf, transform, code := classifySource(opts.Derived, root, adapters.All(opts.Home)[root.Agent], path)
	if code != "" {
		return fail(code)
	}
	if !nativeSessionSource(sf, transform) {
		return fail("native_session_layout_required")
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	return emitRequest(opts, root, sf, path, now().UnixNano(), ""), nil
}

func nativeMissing(native bool, code string) bool { return native && code == "source_unreadable" }

func nativeSessionSource(sf adapters.SessionFile, transform string) bool {
	return sf.SessionKey != "" && (sf.Role == adapters.RoleMain || sf.Role == adapters.RoleSubagent) && transform == ""
}
