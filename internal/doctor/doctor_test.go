package doctor

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/testenv"
)

func recordRuns(t *testing.T, dir string, runs ...state.Run) {
	t.Helper()
	st, err := state.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if err := st.RecordRun(state.UnixNow(), r.Trigger, r.Summary); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func runsReport(t *testing.T, cfg *config.Config) string {
	t.Helper()
	var out bytes.Buffer
	r := &reporter{w: &out}
	checkRuns(t.Context(), cfg, r)
	if r.failed {
		t.Error("run history failed the doctor; it should only warn")
	}
	return out.String()
}

func TestCheckRunsFindsTheLatestSweepBehindManyHookRuns(t *testing.T) {
	cfg := &config.Config{StateDir: t.TempDir()}
	runs := []state.Run{
		{Trigger: "sweep:SessionStart", Summary: map[string]any{"errors": 0}},
		{Trigger: "sweep:SessionEnd", Summary: map[string]any{"errors": 2, "error_messages": []string{"too old to show"}}},
	}
	for range recentRuns {
		runs = append(runs, state.Run{Trigger: "hook:Stop", Summary: map[string]any{"errors": 0}})
	}
	runs = append(runs, state.Run{Trigger: "hook:Stop", Summary: map[string]any{"errors": 1, "error_messages": []string{"from the newest run"}}})
	recordRuns(t, cfg.StateDir, runs...)
	want := "[ok  ] last sweep (sweep:SessionEnd) 0 min ago, 2 errors\n" +
		"[warn] recent errors (see " + filepath.Join(cfg.StateDir, "agent-sessions.log") + "): from the newest run\n"
	if diff := cmp.Diff(want, runsReport(t, cfg)); diff != "" {
		t.Errorf("report (-want +got):\n%s", diff)
	}
}

func TestCheckRunsCountsABackfillAsASweep(t *testing.T) {
	cfg := &config.Config{StateDir: t.TempDir()}
	recordRuns(t, cfg.StateDir, state.Run{Trigger: "backfill", Summary: map[string]any{"errors": 0}})
	if got := runsReport(t, cfg); got != "[ok  ] last sweep (backfill) 0 min ago, 0 errors\n" {
		t.Errorf("report: %q", got)
	}
}

func TestCheckRunsWithoutASweep(t *testing.T) {
	cfg := &config.Config{StateDir: t.TempDir()}
	recordRuns(t, cfg.StateDir, state.Run{Trigger: "hook:Stop", Summary: map[string]any{"errors": 0}})
	want := "[warn] no sweep in the last 500 runs; one runs when a session starts or ends\n"
	if diff := cmp.Diff(want, runsReport(t, cfg)); diff != "" {
		t.Errorf("report (-want +got):\n%s", diff)
	}
}

func TestHooksWhoseBinaryIsGoneFail(t *testing.T) {
	e := testenv.New(t)
	bin := filepath.Join(t.TempDir(), "Downloads", "agent-sessions")
	testenv.Write(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	o := &install.Options{Command: []string{bin}, Home: e.Home}
	for _, installer := range []func(*install.Options) error{install.Claude, install.Codex, install.Pi} {
		if err := installer(o); err != nil {
			t.Fatal(err)
		}
	}
	report := func() string {
		var out bytes.Buffer
		r := &reporter{w: &out}
		checkClaude(e.Home, r)
		checkCodex(e.Home, r)
		checkPi(e.Home, r)
		return out.String()
	}
	if got := report(); strings.Contains(got, "can't be run") {
		t.Errorf("hooks with their binary in place:\n%s", got)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	got := report()
	for _, agent := range []string{"claude", "codex", "pi"} {
		if want := "[FAIL] " + agent + ": hooks run " + bin + ", which can't be run"; !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

func TestProbeStoreWritesReadsAndDeletes(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{MachineID: "testbox", LocalStore: root}
	if err := probeStore(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("probe left %s behind", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	regularFile := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(regularFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := probeStore(t.Context(), &config.Config{MachineID: "testbox", LocalStore: regularFile}); err == nil {
		t.Error("probe of a store under a regular file succeeded")
	}
}
