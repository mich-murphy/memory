package doctor

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/config"

	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestDoctorReportsPersistedPrivacyAndDirectorySkips(t *testing.T) {
	e := testenv.New(t)
	cfg := &config.Config{StateDir: filepath.Join(e.Home, "collector-state")}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordRun(1, "sweep", map[string]any{"errors": 0, "skip_reasons": map[string]int{"literal_secret_assignment": 2, "symlink_directory": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	checkRuns(t.Context(), cfg, &reporter{w: &output})
	for _, want := range []string{"literal_secret_assignment (2)", "symlink_directory (1)"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("doctor missing %s: %s", want, &output)
		}
	}
}
