package capturesource

import (
	"path/filepath"
	"testing"
)

func TestPrimaryResourceAndTranscriptDependenciesRemainRequired(t *testing.T) {
	o := options(t)
	o.Derived = true
	root := filepath.Join(o.Home, ".claude")
	for _, relative := range []string{"projects/slug/session.jsonl", "projects/slug/session/subagents/agent-child.jsonl", "settings.json"} {
		path := fixture(t, filepath.Join(root, relative), "{}\n")
		req, err := Classify(t.Context(), o, "claude", path)
		if err != nil {
			t.Fatal(err)
		}
		if req.Optional {
			t.Fatal("primary resource or conversation dependency became optional")
		}
	}
}
