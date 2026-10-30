package hook

import (
	"context"
	"strings"
	"testing"
)

func TestInspectNativeArgsOnlyAgent(t *testing.T) {
	oldRead, oldParent := readProc, parentPID
	t.Cleanup(func() { readProc, parentPID = oldRead, oldParent })
	parentPID = func() int { return 50 }
	readProc = func(pid int) ([]string, int, bool) {
		if pid == 50 {
			return []string{"sh", "private shell text"}, 51, true
		}
		return []string{"claude", "--prompt", "private prompt", "--model", "sonnet"}, 1, true
	}
	args, ok := InspectNativeArgs(t.Context(), "claude")
	if !ok || strings.Contains(strings.Join(args, " "), "private") {
		t.Fatal(args, ok)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ok := InspectNativeArgs(ctx, "claude"); ok {
		t.Fatal("canceled inspection")
	}
}
