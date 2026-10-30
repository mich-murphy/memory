package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestRuntimeOptionalRepairInspectionAndStoppedReplay(t *testing.T) {
	c, config := runtimeFixture(t)
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	io := IO{Stdout: &output, Stderr: &output}
	if code, err := runCaptureRuntime(t.Context(), "optional-status", []string{"--config", config}, io); err != nil || code != exitOK {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "[]") && !strings.Contains(output.String(), "null") {
		t.Fatal("unexpected empty repair status")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"optional-replay", "--config", config, "--work-id", strings.Repeat("0", 64)}, {"optional-quota-replay", "--config", config, "--agent", "claude", "--session", "missing"}} {
		if _, err := runCaptureRuntime(t.Context(), args[0], args[1:], io); err == nil {
			t.Fatal("unknown retained repair replay succeeded")
		}
	}
	for _, args := range [][]string{{"optional-status", "--config", config, "--limit", "257"}, {"optional-status", "--config", config, "--after-id", "bad"}, {"optional-replay", "--config", config}, {"optional-quota-replay", "--config", config, "--agent", "claude"}, {"optional-status", "--config", config, "--apply"}} {
		if _, err := runCaptureRuntime(t.Context(), args[0], args[1:], io); err == nil {
			t.Fatal("invalid repair flags accepted")
		}
	}
	for _, cmd := range []string{"optional-status", "optional-replay", "optional-quota-replay"} {
		if !runtimeCommand(cmd) {
			t.Fatal("repair command not routed")
		}
	}
}
