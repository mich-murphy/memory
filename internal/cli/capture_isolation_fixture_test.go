package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureIsolationFixtureProcess(t *testing.T) {
	if os.Getenv("MEMORY_ISOLATION_HELPER") == "1" {
		code, err := runCaptureIsolationFixture(t.Context(), os.Args[helperArgs():], IO{Stdout: io.Discard, Stderr: os.Stderr})
		if err != nil {
			t.Fatal(err)
		}
		if code != 0 {
			t.Fatalf("code %d", code)
		}
		return
	}
	root := filepath.Join(t.TempDir(), "owned")
	for _, stage := range []string{"crash", "repair", "revoke"} {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		cmd := isolationCommand(ctx, t, root, stage)
		cmd.Env = append(os.Environ(), "MEMORY_ISOLATION_HELPER=1")
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s: %v\n%s", stage, err, output)
		}
		if stage == "crash" {
			if err := os.RemoveAll(filepath.Join(root, "sources")); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func helperArgs() int {
	for n, arg := range os.Args {
		if arg == "--" {
			return n + 1
		}
	}
	return len(os.Args)
}

// The independent process journey above is mandatory. This second journey also
// exercises the ordinary returning API (cancel/join/Close), which must not leak
// workers merely because the successful crash path intentionally does not return.
func TestCaptureIsolationFixtureReturningStages(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owned")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := isolationCommand(ctx, t, root, "crash")
	cmd.Env = append(os.Environ(), "MEMORY_ISOLATION_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash: %v\n%s", err, output)
	}
	if err := os.RemoveAll(filepath.Join(root, "sources")); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"repair", "revoke"} {
		code, err := runCaptureIsolationFixture(t.Context(), []string{"--output", root, "--stage", stage}, IO{Stdout: io.Discard, Stderr: io.Discard})
		if err != nil || code != 0 {
			t.Fatalf("%s: code %d: %v", stage, code, err)
		}
	}
}

func TestCaptureIsolationFixtureRejectsInvalidInvocation(t *testing.T) {
	for _, args := range [][]string{nil, {"--output", "x", "--stage", "unknown"}, {"--bad"}, {"--output", t.TempDir(), "--stage", "crash"}, {"--output", t.TempDir(), "--stage", "repair"}} {
		code, err := runCaptureIsolationFixture(t.Context(), args, IO{Stdout: io.Discard, Stderr: io.Discard})
		if code == 0 || err == nil {
			t.Fatalf("accepted invalid invocation %v", args)
		}
	}
}

// Returning children flush through M.Run; crash children flush explicitly before
// exit. Both write to the same gate-owned directory, never a nested output tree.
func isolationHelperArgs(root, stage string) []string {
	args := []string{"-test.run=^TestCaptureIsolationFixtureProcess$"}
	if dir := os.Getenv("E2E_GOCOVERDIR"); dir != "" {
		args = append(args, "-test.gocoverdir="+dir)
	}
	return append(args, "--", "--output", root, "--stage", stage)
}

// Test binaries initialize runtime coverage only during M.Run teardown. A crash
// cannot reach that teardown, so use the actual instrumented command for this
// stage instead of unsafe links into Go's private coverage implementation.
func isolationCommand(ctx context.Context, t *testing.T, root, stage string) *exec.Cmd {
	t.Helper()
	if stage != "crash" || os.Getenv("E2E_GOCOVERDIR") == "" {
		return exec.CommandContext(ctx, os.Args[0], isolationHelperArgs(root, stage)...)
	}
	binary := filepath.Join(t.TempDir(), "agent-sessions")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-cover", "-covermode=atomic", "-coverpkg=./...", "-o", binary, "./cmd/agent-sessions")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, output)
	}
	return exec.CommandContext(ctx, binary, "capture-v1", "isolation-fixture", "--output", root, "--stage", stage)
}

func TestCaptureIsolationFixtureCoverageFailures(t *testing.T) {
	for _, kind := range []string{"missing", "not-directory"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "coverage")
			if kind == "not-directory" {
				if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("E2E_GOCOVERDIR", dir)
			cmd := isolationCommand(t.Context(), t, filepath.Join(base, "owned"), "crash")
			cmd.Env = os.Environ()
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "fixture coverage metadata:") {
				t.Fatalf("expected checked coverage failure, got %v: %s", err, output)
			}
		})
	}
}

func TestFixtureCoverageDisabled(t *testing.T) {
	t.Setenv("E2E_GOCOVERDIR", "")
	if err := flushFixtureCoverage(); err != nil {
		t.Fatal(err)
	}
}
