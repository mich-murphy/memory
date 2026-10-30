package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestBrokenPolicyTrustedLifecycle(t *testing.T) {
	c, config := runtimeFixture(t)
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"work-status", "quarantine", "enrichment-status", "optional-status"} {
		t.Run(cmd, func(t *testing.T) {
			if err := os.Remove(filepath.Join(c.PolicyDir, "exclusions")); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if _, err := runCaptureRuntime(t.Context(), cmd, []string{"--config", config}, IO{Stdout: &out, Stderr: &out}); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(filepath.Join(c.PolicyDir, "exclusions"), 0o700); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := os.Remove(filepath.Join(c.PolicyDir, "exclusions")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"service", []string{"--action", "stop"}},
		{"service", []string{"--action", "uninstall"}},
		{"hooks", []string{"--action", "uninstall"}},
	} {
		t.Run(tc.cmd+tc.args[1], func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", config, "--executable", filepath.Join(c.Home, "missing-old-binary")}, tc.args...)
			if _, err := runCaptureRuntime(t.Context(), tc.cmd, args, IO{Stdout: &out, Stderr: &out}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, cmd := range []string{"run", "hook"} {
		if _, err := runCaptureRuntime(t.Context(), cmd, []string{"--config", config}, IO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}); err == nil {
			t.Fatal("activation accepted missing policy")
		}
	}
}

func TestServiceActivationRequiresExecutable(t *testing.T) {
	c, config := runtimeFixture(t)
	// A regression must not be capable of invoking the real host supervisor.
	t.Setenv("PATH", c.Home)
	for _, action := range []string{"install", "start", "restart"} {
		var out bytes.Buffer
		if _, err := runCaptureRuntime(t.Context(), "service", []string{"--config", config, "--action", action, "--apply", "--executable", filepath.Join(c.Home, "missing-old-binary")}, IO{Stdout: &out, Stderr: &out}); err == nil || !strings.Contains(err.Error(), "candidate executable unavailable or unsafe") {
			t.Fatal("activation did not reject missing executable before supervisor", err)
		}
	}
}

func TestDeclaredAliasNativeHookTargets(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-logical", true: "linked-logical"}[linked], func(t *testing.T) {
			c, config := runtimeFixture(t)
			logical := filepath.Join(c.Home, ".claude")
			physical := filepath.Join(c.Home, "relocated")
			if err := os.MkdirAll(physical, 0o700); err != nil {
				t.Fatal(err)
			}
			if linked {
				if err := os.Symlink(physical, logical); err != nil {
					t.Fatal(err)
				}
			}
			c.DefaultRoots = false
			c.Roots = []capturedaemon.Root{{Agent: "claude", Path: logical}}
			c.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: logical, Path: physical}}
			if err := capturedaemon.Save(config, c); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(c.Home, "candidate")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if _, err := runCaptureRuntime(t.Context(), "hooks", []string{"--config", config, "--executable", exe, "--action", "install", "--apply"}, IO{Stdout: &out, Stderr: &out}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(physical, "settings.json")); err != nil {
				t.Fatal("hook not installed at physical coordinate", err)
			}
			if !linked {
				if _, err := os.Stat(logical); !os.IsNotExist(err) {
					t.Fatal("created missing logical root")
				}
			}
		})
	}
}
