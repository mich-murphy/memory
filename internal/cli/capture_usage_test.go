package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCaptureBackendRootUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"help", []string{"--help"}, exitOK},
		{"missing_command", nil, exitUsage},
		{"unknown_command", []string{"unknown-command"}, exitUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			code := Main(t.Context(), tc.args, "test", IO{Stdout: &output, Stderr: &output})
			if code != tc.code {
				t.Fatalf("exit code %d, want %d", code, tc.code)
			}
			text := output.String()
			if !strings.Contains(text, "capture-v1   experimental opt-in") || !strings.Contains(text, "Local or configured R2") {
				t.Fatalf("usage omits explicit candidate/backend boundary: %s", text)
			}
			if strings.Contains(text, "local store only") {
				t.Fatal("usage incorrectly restricts implemented configured backends to Local")
			}
		})
	}
}
