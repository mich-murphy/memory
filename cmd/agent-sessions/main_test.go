package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
)

// binDir holds the agent-sessions binary built for the scripts, with coverage on so its runs
// count towards the coverage gate (when E2E_GOCOVERDIR names where to put the data).
var binDir string

func TestMain(m *testing.M) {
	os.Exit(buildAndRun(m))
}

func buildAndRun(m *testing.M) int {
	dir, err := os.MkdirTemp("", "agent-sessions-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }() // a leftover temp dir is harmless
	binDir = dir
	build := exec.CommandContext(context.Background(), "go", "build", "-cover", "-covermode=atomic", "-coverpkg=github.com/mich-murphy/memory/...", "-o",
		filepath.Join(dir, "agent-sessions"), ".")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building agent-sessions:", err)
		return 1
	}
	return m.Run()
}

func TestScripts(t *testing.T) {
	coverDir := os.Getenv("E2E_GOCOVERDIR")
	if coverDir == "" {
		coverDir = t.TempDir() // a -cover binary warns on stderr without one
	}
	writeCoverMetaFile(t, coverDir)
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			env.Setenv("PATH", binDir+string(os.PathListSeparator)+env.Getenv("PATH"))
			env.Setenv("GOCOVERDIR", coverDir)
			home := filepath.Join(env.WorkDir, "home")
			env.Setenv("HOME", home)
			env.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(env.WorkDir, "config.env"))
			env.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Join(env.WorkDir, "state"))
			env.Setenv("AGENT_SESSIONS_LOCAL_STORE", filepath.Join(env.WorkDir, "bucket"))
			env.Setenv("AGENT_SESSIONS_MACHINE_ID", "testbox")
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"waitfor": waitFor,
			"age":     age,
		},
	})
}

// Each -cover process that finds no meta-data file writes one through a temporary named by its
// start time, and on macOS's microsecond clock two scripts starting together collide.
func writeCoverMetaFile(t *testing.T, coverDir string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), filepath.Join(binDir, "agent-sessions"), "--version")
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("agent-sessions --version: %v\n%s", err, out)
	}
}

// waitfor GLOB [SECONDS] waits for a file matching GLOB to appear (default 20 s); negated, for
// every match to go.
func waitFor(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) < 1 || len(args) > 2 {
		ts.Fatalf("usage: waitfor path [seconds]")
	}
	limit := 20 * time.Second
	if len(args) == 2 {
		n, err := strconv.Atoi(args[1])
		ts.Check(err)
		limit = time.Duration(n) * time.Second
	}
	pattern := ts.MkAbs(args[0])
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		matches, err := filepath.Glob(pattern)
		ts.Check(err)
		if (len(matches) > 0) != neg {
			return
		}
	}
	ts.Fatalf("waited %v for %s", limit, pattern)
}

// age SECONDS PATH... sets the files' mtimes that long ago.
func age(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 2 {
		ts.Fatalf("usage: age seconds path...")
	}
	n, err := strconv.Atoi(args[0])
	ts.Check(err)
	when := time.Now().Add(-time.Duration(n) * time.Second)
	for _, p := range args[1:] {
		ts.Check(os.Chtimes(ts.MkAbs(p), when, when))
	}
}
