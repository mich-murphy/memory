package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
		binary := isolationCoverageBinary(t, stage)
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		started := time.Now()
		cmd := isolationCommand(ctx, binary, root, stage)
		setupElapsed := time.Since(started)
		cmd.Env = append(os.Environ(), "MEMORY_ISOLATION_HELPER=1")
		output, err := cmd.CombinedOutput()
		contextErr := ctx.Err()
		cancel()
		if err != nil {
			t.Fatalf("%s: %v (setup %s, total %s, context %v)\n%s", stage, err, setupElapsed, time.Since(started), contextErr, output)
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
	binary := isolationCoverageBinary(t, "crash")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	started := time.Now()
	cmd := isolationCommand(ctx, binary, root, "crash")
	setupElapsed := time.Since(started)
	cmd.Env = append(os.Environ(), "MEMORY_ISOLATION_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash: %v (setup %s, total %s, context %v)\n%s", err, setupElapsed, time.Since(started), ctx.Err(), output)
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
// The compile is test-harness setup, not fixture behavior: it runs before the
// caller starts the stage deadline, so that deadline bounds only the fixture run.
// Empty means the stage runs as a test-binary helper.
func isolationCoverageBinary(t *testing.T, stage string) string {
	t.Helper()
	if stage != "crash" || os.Getenv("E2E_GOCOVERDIR") == "" {
		return ""
	}
	isolationBuild.once.Do(buildIsolationCoverageBinary)
	if isolationBuild.err != nil {
		t.Fatal(isolationBuild.err)
	}
	return isolationBuild.binary
}

// One instrumented build per test binary, outside every stage deadline. The
// directory outlives individual tests and is removed by TestMain.
var isolationBuild struct {
	once   sync.Once
	dir    string
	binary string
	err    error
}

func buildIsolationCoverageBinary() {
	dir, err := os.MkdirTemp("", "isolation-fixture-")
	if err != nil {
		isolationBuild.err = err
		return
	}
	isolationBuild.dir = dir
	binary := filepath.Join(dir, "agent-sessions")
	build := exec.CommandContext(context.Background(), "go", "build", "-race", "-cover", "-covermode=atomic", "-coverpkg=./...", "-o", binary, "./cmd/agent-sessions")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		isolationBuild.err = fmt.Errorf("build crash fixture: %w\n%s", err, output)
		return
	}
	isolationBuild.binary = binary
}

func TestMain(m *testing.M) {
	code := m.Run()
	if isolationBuild.dir != "" {
		_ = os.RemoveAll(isolationBuild.dir)
	}
	os.Exit(code)
}

func isolationCommand(ctx context.Context, binary, root, stage string) *exec.Cmd {
	if binary == "" {
		return exec.CommandContext(ctx, os.Args[0], isolationHelperArgs(root, stage)...)
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
			fixtureRoot := filepath.Join(base, "owned")
			cmd := isolationCommand(t.Context(), isolationCoverageBinary(t, "crash"), fixtureRoot, "crash")
			cmd.Env = os.Environ()
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "fixture coverage metadata:") {
				t.Fatalf("expected checked coverage failure, got %v: %s", err, output)
			}
			if _, err := os.Lstat(fixtureRoot); !os.IsNotExist(err) {
				t.Fatalf("coverage failure must precede fixture side effects: Lstat = %v", err)
			}
		})
	}
}

// This test-binary child exercises the real final exit boundary directly, not
// command coverage initialization (which the actual-command journeys prove).
// A failed final flush must return its diagnostic rather than exit successfully.
func TestCaptureIsolationFixtureFinalCoverageFailure(t *testing.T) {
	if os.Getenv("MEMORY_ISOLATION_EXIT_HELPER") == "1" {
		if err := isolationExit(); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, kind := range []string{"missing", "not-directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "coverage")
			if kind == "not-directory" {
				if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-test.run=^TestCaptureIsolationFixtureFinalCoverageFailure$"}
			// Keep M.Run's actual subprocess coverage in the gate-owned valid
			// directory, independent of the intentionally bad fixture setting.
			if coverDir := os.Getenv("E2E_GOCOVERDIR"); coverDir != "" {
				args = append(args, "-test.gocoverdir="+coverDir)
			}
			cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
			cmd.Env = append(os.Environ(), "MEMORY_ISOLATION_EXIT_HELPER=1", "E2E_GOCOVERDIR="+dir)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "fixture coverage metadata:") {
				t.Fatalf("expected checked final coverage failure, got %v: %s", err, output)
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
