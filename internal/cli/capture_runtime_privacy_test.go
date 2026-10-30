package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func runtimeWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePrivacyIndependentSessionsAndDeletedOriginal(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "custom"}[custom], func(t *testing.T) {
			c, _ := runtimeFixture(t)
			root := filepath.Join(c.Home, ".claude")
			if custom {
				root = filepath.Join(c.Home, "learned")
				c.DefaultRoots = false
				c.Roots = []capturedaemon.Root{{Agent: "claude", Path: root}}
			}
			a := filepath.Join(root, "projects", "p", "A.jsonl")
			as := filepath.Join(root, "projects", "p", "A", "subagents", "agent-x.jsonl")
			b := filepath.Join(root, "projects", "p", "B.jsonl")
			bs := filepath.Join(root, "projects", "p", "B", "subagents", "agent-y.jsonl")
			for _, p := range []string{a, as, b, bs} {
				runtimeWrite(t, p)
			}
			discover, closeDiscovery := runtimeDiscovery(c, "")
			defer func() {
				if err := closeDiscovery(); err != nil {
					t.Error(err)
				}
			}()
			batch, err := discover(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := q.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := publication.WriteArchive(t.Context(), publication.NewLocal(c.Store), publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
				t.Fatal(err)
			}
			requests := map[string]sessioncapture.Request{}
			for _, r := range batch.Requests {
				requests[r.Path] = r
			}
			for _, p := range []string{a, as, b, bs} {
				r, ok := requests[p]
				if !ok {
					t.Fatal("missing initial discovery")
				}
				if _, err := q.Capture(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			}
			adapter := adapters.NewClaude(c.Home)
			// RecordPath supports explicit learned roots through inference; use its
			// returned legacy record, not the new root-qualified request session ID.
			sf, ok := adapter.Classify(a, root)
			if !ok {
				t.Fatal("classification")
			}
			record, err := exclusion.RecordPath(c.PolicyDir, adapter, a)
			if err != nil {
				t.Fatal(err)
			}
			if record.SessionKey != sf.SessionKey {
				t.Fatal("legacy policy ownership mismatch")
			}
			for _, p := range []string{a, as} {
				if err := runtimePolicy(c)(t.Context(), requests[p]); err == nil {
					t.Fatal("opted-out session admitted")
				}
			}
			if err := os.Remove(a); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(as); err != nil {
				t.Fatal(err)
			}
			if err := q.Deliver(t.Context(), publication.NewLocal(c.Store), requests[a].Identity); err == nil {
				t.Fatal("deleted excluded original delivered")
			}
			for _, p := range []string{b, bs} {
				if err := runtimePolicy(c)(t.Context(), requests[p]); err != nil {
					t.Fatal(err)
				}
				if _, err := q.Capture(t.Context(), requests[p]); err != nil {
					t.Fatal("healthy session capture blocked after opt-out:", err)
				}
			}
			if err := q.Deliver(t.Context(), publication.NewLocal(c.Store), requests[b].Identity); err != nil {
				t.Fatal(err)
			}
			next, closeNext := runtimeDiscovery(c, "")
			defer func() {
				if err := closeNext(); err != nil {
					t.Error(err)
				}
			}()
			found := map[string]bool{}
			// Discovery is intentionally budgeted: continue until coverage completes.
			for range 20 {
				batch, err := next(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range batch.Requests {
					found[r.Path] = true
				}
				if batch.Complete {
					break
				}
			}
			if !found[b] || !found[bs] {
				t.Fatal("one opt-out blocked healthy session discovery")
			}
			if found[a] || found[as] {
				t.Fatal("excluded session discovered")
			}
		})
	}
}

func TestRuntimePrivacyScopesAggregatesAndUntrustedIdentity(t *testing.T) {
	c, _ := runtimeFixture(t)
	root := filepath.Join(c.Home, ".claude")
	a := filepath.Join(root, "projects", "p", "A.jsonl")
	runtimeWrite(t, a)
	if _, err := exclusion.RecordPath(c.PolicyDir, adapters.NewClaude(c.Home), a); err != nil {
		t.Fatal(err)
	}
	identity := publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude", SessionID: "forged-healthy"}
	check := func(path, role string, want bool) {
		t.Helper()
		err := runtimeDiscoveryPolicy(c)(t.Context(), sessioncapture.Request{Identity: identity, Path: path, Role: role})
		if (err == nil) != want {
			t.Fatalf("privacy decision for %s role %q: %v", filepath.Base(path), role, err)
		}
	}
	check(root, "", true)
	check(filepath.Join(root, "projects", "p"), "", true)
	check(root, "artifact", false)
	check(a, "main", false)
	for _, rel := range []string{"history.jsonl", "state.db", "state.sqlite", "state.sqlite3", "settings.json"} {
		p := filepath.Join(root, rel)
		runtimeWrite(t, p)
		check(p, "artifact", false)
	}
	global := filepath.Join(c.Home, ".claude.json")
	runtimeWrite(t, global)
	check(global, "artifact", false)
	// These paths must be rejected before adapter classification can index all
	// projects. They are unsupported even without a matching session/record.
	for _, top := range []string{"file-history", "session-env", "debug", "todos", "tasks"} {
		p := filepath.Join(root, top, "12345678-1234-1234-1234-123456789abc", "x")
		runtimeWrite(t, p)
		check(p, "artifact", false)
	}
}

func TestRuntimePrivacyFailClosedAndConfigurationIdentity(t *testing.T) {
	c, _ := runtimeFixture(t)
	path := filepath.Join(c.Home, ".claude", "projects", "p", "B.jsonl")
	runtimeWrite(t, path)
	r := sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude", SessionID: "B"}, Path: path, Role: "main"}
	policy := runtimePolicy(c)
	t.Setenv("AGENT_SESSIONS_DISABLE", "1")
	if policy(t.Context(), r) == nil {
		t.Fatal("global disable ignored")
	}
	t.Setenv("AGENT_SESSIONS_DISABLE", "")
	broken := filepath.Join(c.PolicyDir, "exclusions", "bad.json")
	if err := os.WriteFile(broken, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if policy(t.Context(), r) == nil {
		t.Fatal("unreadable policy admitted")
	}
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	// A runtime config disguised as a supported transcript is still protected.
	if err := capturedaemon.Save(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := capturedaemon.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Transform = "scrubbed-config"
	for _, p := range []func(capturedaemon.Config) func(context.Context, sessioncapture.Request) error{runtimePolicy, runtimeDiscoveryPolicy} {
		if p(loaded)(t.Context(), r) == nil {
			t.Fatal("runtime configuration admitted")
		}
	}
	if err := os.Remove(filepath.Join(c.PolicyDir, "exclusions")); err != nil {
		t.Fatal(err)
	}
	if policy(t.Context(), r) == nil {
		t.Fatal("missing policy admitted")
	}
}

func TestRuntimePrivacyLongestConfiguredRootAndGlobalException(t *testing.T) {
	c, _ := runtimeFixture(t)
	global := filepath.Join(c.Home, ".claude.json")
	runtimeWrite(t, global)
	r := sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude", SessionID: "untrusted"}, Path: global, Role: "artifact", Transform: "scrubbed-config"}
	if err := runtimePolicy(c)(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if runtimeOwningRoot(c, "claude", global) != filepath.Join(c.Home, ".claude") {
		t.Fatal("global namespace")
	}
	c.Roots = []capturedaemon.Root{{Agent: "claude", Path: c.Home}}
	if runtimeOwningRoot(c, "claude", global) != c.Home {
		t.Fatal("actual owner lost to namespace exception")
	}
	nested := filepath.Join(c.Home, "nested")
	c.Roots = append(c.Roots, capturedaemon.Root{Agent: "claude", Path: nested})
	p := filepath.Join(nested, "projects", "p", "S.jsonl")
	runtimeWrite(t, p)
	r.Path = p
	sf, err := runtimePrivacyOwner(t.Context(), c, r, false)
	if err != nil || sf.Root != nested || sf.SessionKey == "" {
		t.Fatal("longest root ownership failed")
	}
	c.DefaultRoots = false
	c.Roots = nil
	if c.AllowsSource("claude", global) {
		t.Fatal("default exception survived disabling defaults")
	}
	c.DefaultRoots = true
	if c.AllowsSource("claude", filepath.Join(c.Home, "unrelated.json")) {
		t.Fatal("global file exception broadened home inventory")
	}
	for _, root := range c.SourceRoots() {
		if root.Path == c.Home {
			t.Fatal("global file exception exposed home root")
		}
	}
}

func TestRuntimeServiceCLIValidationAndDryLifecycle(t *testing.T) {
	c, config := runtimeFixture(t)
	var out bytes.Buffer
	io := IO{Stdout: &out, Stderr: &out}
	base := runtimeFlags{config: config, platform: "linux", executable: filepath.Join(c.Home, "candidate")}
	for _, action := range []string{"install", "start", "stop", "restart", "uninstall"} {
		f := base
		f.action = action
		if code, err := runCaptureService(t.Context(), c, f, io); err != nil || code != exitOK {
			t.Fatalf("dry lifecycle %s: %v", action, err)
		}
	}
	for _, f := range []runtimeFlags{
		{action: "unknown"},
		{action: "plan", apply: true},
		{action: "install", platform: "other-host", apply: true},
		{action: "plan", platform: "unsupported", executable: base.executable, config: config},
	} {
		if code, err := runCaptureService(t.Context(), c, f, io); err == nil || code != exitUsage {
			t.Fatal("unsafe service arguments admitted")
		}
	}
	if _, err := os.Stat(filepath.Join(c.Home, ".config")); !os.IsNotExist(err) {
		t.Fatal("dry lifecycle mutated host")
	}
}
