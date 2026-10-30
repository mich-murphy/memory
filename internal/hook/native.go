package hook

import (
	"context"

	"github.com/mich-murphy/memory/internal/launch"
)

// InspectNativeArgs inspects only transient ancestors and returns agent metadata.
// Shell/collector/intermediate argv and environment are never returned.
func InspectNativeArgs(ctx context.Context, agent string) ([]string, bool) {
	if _, ok := agentMarkers[agent]; !ok {
		return nil, false
	}
	pid := parentPID()
	for range maxAncestors {
		if ctx.Err() != nil || pid <= 1 {
			break
		}
		argv, next, ok := readProc(pid)
		if ctx.Err() != nil || !ok {
			break
		}
		if isAgent(agent, argv) {
			return launch.SanitizeNativeArgs(argv), true
		}
		if next == pid {
			break
		}
		pid = next
	}
	return nil, false
}
