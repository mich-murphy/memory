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
		state.Run{Trigger: "sweep", Summary: map[string]any{"errors": 2, "error_messages": []string{"from the latest sweep"}}},
		state.Run{Trigger: "hook", Summary: map[string]any{"errors": 1, "error_messages": []string{"from the newest run"}}},
	)
	want := "[ok  ] last sweep 0 min ago, 2 errors\n" +
		"[warn] recent errors (see " + filepath.Join(cfg.StateDir, "agent-sessions.log") + "): from the newest run\n"
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
	if got := runsReport(t, cfg, ""); got != "" {
		t.Errorf("without a timer, which already fails the doctor: %q", got)
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
