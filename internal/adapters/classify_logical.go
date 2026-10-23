package adapters

import "strings"

// ClassifyLogical classifies an already admitted declared coordinate without
// consulting whether its logical root still exists. Pi's legacy classifier uses
// directory metadata to distinguish agent state from a flat session directory;
// declared session/resource layouts provide the same structural proof here.
func ClassifyLogical(a Adapter, path, root string) (SessionFile, bool) {
	if pi, ok := a.(*Pi); ok {
		rel, inside := relative(path, root)
		if !inside {
			return SessionFile{}, false
		}
		agentRoot := root == pi.CanonicalRoot() || strings.HasPrefix(rel, "sessions/") || topRole(piRoles, rel) != RoleUnknown
		return pi.classifyLayout(path, root, agentRoot)
	}
	return a.Classify(path, root)
}
