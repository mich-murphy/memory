package capturesource

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDerivedHintsAndOwnership(t *testing.T) {
	o := options(t)
	o.Derived = true
	o.Now = func() time.Time { return time.Unix(123, 456) }
	parent := "2026-01-01T00-00-00Z_11111111-1111-1111-1111-111111111111"
	for _, custom := range []bool{false, true} {
		for _, agent := range []string{"claude", "codex", "pi"} {
			root := filepath.Join(o.Home, "."+agent)
			if agent == "pi" {
				root = filepath.Join(root, "agent")
			}
			if custom {
				root = filepath.Join(o.Home, "custom-"+agent)
				o.Roots = append(o.Roots, Root{agent, root})
			}
			paths := []string{"config.toml", "state.sqlite", "notes.txt"}
			switch agent {
			case "claude":
				paths = []string{"settings.json", "state.db", "notes.txt", "projects/p/s.jsonl", "projects/p/s.jsonl.zst", "projects/p/s/subagents/c.jsonl.zst", "projects/p/s/state.db"}
			case "codex":
				paths = append(paths, "sessions/2026/rollout-2026-01-01T00-00-00-11111111-1111-1111-1111-111111111111.jsonl.zst")
			case "pi":
				paths = []string{"settings.json", "state.sqlite3", "notes.txt", "sessions/--project--/" + parent + ".jsonl.zst", "sessions/--project--/" + parent + "/tasks/c.jsonl.zst"}
			}
			for _, p := range paths {
				fixture(t, filepath.Join(root, p), "intentionally unparsed metadata-only fixture")
			}
		}
	}
	r := discover(t, o)
	if len(r.Requests) != 32 {
		t.Fatalf("requests=%d issues=%v", len(r.Requests), r.Issues)
	}
	transforms := map[string]int{}
	owners := map[string]string{}
	for _, q := range r.Requests {
		got, err := Classify(t.Context(), o, q.Identity.Agent, q.Path)
		if err != nil || !reflect.DeepEqual(got, q) {
			t.Fatal(q, got, err)
		}
		transforms[q.Transform]++
		if q.Identity.Agent == "claude" && strings.Contains(q.Path, "/projects/p/s") {
			root, _, _ := strings.Cut(q.Path, "/projects/p/s")
			if old := owners[root]; old != "" && old != q.Identity.SessionID {
				t.Fatal("lost DB/child session ownership", q)
			}
			owners[root] = q.Identity.SessionID
		}
		if q.Transform == "sqlite-backup" {
			if !strings.Contains(q.SourceID, "db/") || !slices.Contains(q.Associations, "adapter-role:db") {
				t.Fatal("lost adapter-derived DB identity", q)
			}
		}
		if q.Transform == "scrubbed-config" && !slices.Contains(q.Associations, "adapter-role:config") {
			t.Fatal("lost config role", q)
		}
		if q.Transform != "" {
			found := false
			for _, a := range q.Associations {
				found = found || a == "transform:"+q.Transform
			}
			if !found {
				t.Fatal("missing transform provenance", q)
			}
		}
		if q.Transform == "sqlite-backup" || q.Transform == "scrubbed-config" {
			if q.Role != "artifact" {
				t.Fatal(q)
			}
		}
		if q.Transform == "zstd" && q.Role != "main" && q.Role != "subagent" {
			t.Fatal(q)
		}
	}
	if transforms["zstd"] != 10 || transforms["sqlite-backup"] != 8 || transforms["scrubbed-config"] != 6 || transforms[""] != 8 {
		t.Fatal(transforms)
	}
}

func TestDerivedPhysicalVsExplicitAdapterMapping(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "pi"} {
		o := options(t)
		o.Derived = true
		root := filepath.Join(o.Home, "."+agent)
		rel := "projects/p/s.jsonl"
		switch agent {
		case "codex":
			rel = "sessions/2026/rollout-2026-01-01T00-00-00-11111111-1111-1111-1111-111111111111.jsonl"
		case "pi":
			root = filepath.Join(root, "agent")
			rel = "sessions/--project--/s.jsonl"
		}
		p := fixture(t, filepath.Join(root, rel), "raw")
		z := fixture(t, p+".zst", "not decoded by discovery")
		raw, err := Classify(t.Context(), o, agent, p)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := Classify(t.Context(), o, agent, z)
		if err != nil {
			t.Fatal(err)
		}
		if raw.Identity != encoded.Identity {
			t.Fatal("lost session owner", raw, encoded)
		}
		if (raw.SourceID == encoded.SourceID) != (agent == "codex") {
			t.Fatal("inferred source merge", raw, encoded)
		}
	}
}

func TestDerivedUnsupportedAndOutsideScope(t *testing.T) {
	o := options(t)
	o.Derived = true
	root := filepath.Join(o.Home, ".claude")
	paths := map[string]string{
		"unknown.zst": "unsupported_compressed_source", "projects/p/s/tool-results/r.jsonl.zst": "unsupported_compressed_source",
		"settings.json.zst": "protected_compressed_source_requires_decode_scrub", "CLAUDE.md": "protected_source_requires_scrub",
		"tasks/11111111-1111-1111-1111-111111111111/state.db": "ownership_integration_required", "auth.json": "policy_denied",
	}
	for rel, code := range paths {
		p := fixture(t, filepath.Join(root, rel), "unparsed")
		_, err := Classify(t.Context(), o, "claude", p)
		var gap *ClassificationError
		if !errors.As(err, &gap) || gap.Issue.Code != code {
			t.Fatal(rel, err)
		}
	}
	outside := fixture(t, filepath.Join(o.Home, ".claude.json"), "{}")
	q, err := Classify(t.Context(), o, "claude", outside)
	if err != nil || q.SourceID != "config/claude.json" || q.Transform != "scrubbed-config" {
		t.Fatal(q, err)
	}
	var gap *ClassificationError
	r := discover(t, o)
	if len(r.Requests) != 1 || hasCode(r, "protected_export_integration_required") {
		t.Fatal(r)
	}

	// An explicitly scoped in-root primary config can use the exact scrubbed
	// exception, but credential backups cannot; this never widens Home inventory.
	p := fixture(t, filepath.Join(root, ".claude.json"), "{}")
	q, err = Classify(t.Context(), o, "claude", p)
	if err != nil || q.Transform != "scrubbed-config" || !strings.HasPrefix(q.Identity.SessionID, "resource/") {
		t.Fatal(q, err)
	}
	p = fixture(t, p+".bak", "{}")
	_, err = Classify(t.Context(), o, "claude", p)
	if !errors.As(err, &gap) || gap.Issue.Code != "policy_denied" {
		t.Fatal(err)
	}
}

func TestDerivedPolicyAndAliasFailClosed(t *testing.T) {
	o := options(t)
	o.Derived = true
	root := filepath.Join(o.Home, ".claude")
	config := fixture(t, filepath.Join(root, "settings.json"), "{}")
	alias := filepath.Join(root, "safe.json")
	if err := os.Symlink(config, alias); err != nil {
		t.Fatal(err)
	}
	_, err := Classify(t.Context(), o, "claude", alias)
	var gap *ClassificationError
	if !errors.As(err, &gap) || gap.Issue.Code != "alias_integration_required" {
		t.Fatal(err)
	}
	o.ConfigPath = config
	_, err = Classify(t.Context(), o, "claude", config)
	if !errors.As(err, &gap) || gap.Issue.Code != "policy_denied" {
		t.Fatal(err)
	}
	primary := fixture(t, filepath.Join(root, ".claude.json"), "{}")
	o.ConfigPath = primary
	_, err = Classify(t.Context(), o, "claude", primary)
	if !errors.As(err, &gap) || gap.Issue.Code != "policy_denied" {
		t.Fatal("scrub must not bypass active collector config", err)
	}
	o.ConfigPath = ""
	o.Policy = nil
	_, err = Classify(t.Context(), o, "claude", config)
	if !errors.As(err, &gap) || gap.Issue.Code != "policy_unavailable" {
		t.Fatal(err)
	}
}
