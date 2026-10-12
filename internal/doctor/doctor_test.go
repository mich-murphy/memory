package doctor

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/state"
)

func recordRuns(t *testing.T, dir string, runs ...state.Run) {
	t.Helper()
	st, err := state.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if err := st.RecordRun(state.Now(), r.Trigger, r.Summary); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func runsReport(t *testing.T, cfg *config.Config, timer string) string {
	t.Helper()
	var out bytes.Buffer
	r := &reporter{w: &out}
	checkRuns(t.Context(), cfg, timer, r)
	if r.failed {
		t.Error("run history failed the doctor; it should only warn")
	}
	return out.String()
}

func TestCheckRunsReportsTheLatestSweepAndFirstRecentError(t *testing.T) {
	cfg := &config.Config{StateDir: t.TempDir()}
	recordRuns(t, cfg.StateDir,
		state.Run{Trigger: "sweep", Summary: map[string]any{"errors": 0}},
		state.Run{Trigger: "sweep", Summary: map[string]any{"errors": 2, "error_messages": []string{"first", "second"}}},
		state.Run{Trigger: "hook", Summary: map[string]any{"errors": 1, "error_messages": []string{"newest"}}},
	)
	// Runs are read newest first: the hook's error leads, and the sweep is the second one recorded.
	want := "[ok  ] last sweep 0 min ago, 2 errors\n" +
		"[warn] recent errors (see " + filepath.Join(cfg.StateDir, "agent-sessions.log") + "): newest\n"
	if diff := cmp.Diff(want, runsReport(t, cfg, "systemd")); diff != "" {
		t.Errorf("report (-want +got):\n%s", diff)
	}
}

func TestCheckRunsWithoutHistory(t *testing.T) {
	cfg := &config.Config{StateDir: t.TempDir()}
	recordRuns(t, cfg.StateDir, state.Run{Trigger: "hook", Summary: map[string]any{"errors": 0}})
	if diff := cmp.Diff("[warn] no sweep has run yet\n", runsReport(t, cfg, "cron")); diff != "" {
		t.Errorf("with a timer (-want +got):\n%s", diff)
	}
	// Without a timer the missing timer is already a failure; no second line about sweeps.
	if got := runsReport(t, cfg, ""); got != "" {
		t.Errorf("without a timer: %q", got)
	}
}

func TestProbeStoreWritesReadsAndDeletes(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{MachineID: "testbox", LocalStore: root}
	if err := probeStore(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	// The probe cleans up after itself: only empty directories may remain.
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("probe left %s behind", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A store that can't be written to fails the probe.
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := probeStore(t.Context(), &config.Config{MachineID: "testbox", LocalStore: blocked}); err == nil {
		t.Error("probe of a store under a regular file succeeded")
	}
}
