package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestFailedRecoveryIsLoggedAndRecorded(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(strconv.FormatBool(quiet), func(t *testing.T) {
			e := testenv.New(t)
			testenv.Write(t, filepath.Join(e.Bucket, "v3", "machines", testenv.MachineID, "checkpoint.json"), `{}`)
			args := []string{"sync", "--agent", "pi", "--path", filepath.Join(e.PiDir, testenv.PiKey+".jsonl"), "--trigger", "hook:test"}
			if quiet {
				args = append(args, "--quiet")
			}
			if code, _, errOut := run(args...); code != 1 {
				t.Fatalf("sync = %d, %s, want failure", code, errOut)
			}
			body, err := os.ReadFile(filepath.Join(e.State, "agent-sessions.log"))
			if err != nil {
				t.Fatal(err)
			}
			log := string(body)
			for _, want := range []string{`msg="sync start"`, `agents=pi`, `paths=1`, `msg="sync failed"`, `level=ERROR`, `trigger=hook:test`, fmt.Sprintf("pid=%d", os.Getpid()), "archive checkpoint: invalid identity"} {
				if !strings.Contains(log, want) {
					t.Errorf("log = %s, want %q", log, want)
				}
			}
			if strings.Contains(log, `msg="sync done"`) {
				t.Errorf("failed sync logged success: %s", log)
			}
			if strings.Contains(log, e.PiDir) || strings.Contains(log, testenv.PiKey) {
				t.Errorf("start/failure logging exposed the requested source path: %s", log)
			}
			db, err := state.Open(t.Context(), e.State)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			runs, err := db.LastRuns(10)
			if err != nil || len(runs) != 1 {
				t.Fatalf("LastRuns = %v, %v, want one failed run", runs, err)
			}
			r := runs[0]
			if r.Trigger != "hook:test" || r.Summary["errors"] != json.Number("1") || r.StartedAt <= 0 || r.FinishedAt < r.StartedAt {
				t.Errorf("failed run = %+v", r)
			}
			if !strings.Contains(fmt.Sprint(r.Summary["error_messages"]), "archive checkpoint: invalid identity") {
				t.Errorf("failed run summary = %v, want recovery error", r.Summary)
			}
		})
	}
}

func TestCompletedSyncRecordsOneRunAndLogsItsOutcome(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(strconv.FormatBool(failed), func(t *testing.T) {
			e := testenv.New(t)
			if failed {
				testenv.Write(t, filepath.Join(e.State, "pending", "launch-bad.json"), "not json")
			}
			code, _, errOut := run("sync", "--quiet", "--agent", "pi", "--path", filepath.Join(e.PiDir, testenv.PiKey+".jsonl"))
			if (code == 1) != failed || code > 1 {
				t.Fatalf("sync = %d, %s, failed = %v", code, errOut, failed)
			}
			body, err := os.ReadFile(filepath.Join(e.State, "agent-sessions.log"))
			if err != nil {
				t.Fatal(err)
			}
			log := string(body)
			if strings.Contains(log, `msg="sync failed"`) != failed || strings.Contains(log, `msg="sync done"`) == failed {
				t.Errorf("log = %s, failed = %v", log, failed)
			}
			db, err := state.Open(t.Context(), e.State)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			runs, err := db.LastRuns(10)
			if err != nil || len(runs) != 1 {
				t.Fatalf("LastRuns = %v, %v, want one run", runs, err)
			}
			want := json.Number("0")
			if failed {
				want = "1"
			}
			if runs[0].Summary["errors"] != want {
				t.Errorf("run summary = %v, want errors = %v", runs[0].Summary, want)
			}
		})
	}
}

func TestStateOpenFailureIsLogged(t *testing.T) {
	e := testenv.New(t)
	if err := os.MkdirAll(filepath.Join(e.State, "state.sqlite3"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run("sync", "--quiet"); code != 1 {
		t.Fatalf("sync = %d, %s, want failure", code, errOut)
	}
	body, err := os.ReadFile(filepath.Join(e.State, "agent-sessions.log"))
	if err != nil {
		t.Fatal(err)
	}
	if log := string(body); !strings.Contains(log, `msg="sync start"`) || !strings.Contains(log, `msg="sync failed"`) || !strings.Contains(log, "level=ERROR") {
		t.Errorf("log = %s, want start and failed", log)
	}
}
