package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
	"github.com/mich-murphy/memory/internal/snapshot"
)

func aliasEnrichmentFixture(t *testing.T) (capturedaemon.Config, sessioncapture.EnrichmentSpec, string) {
	t.Helper()
	c, s := runtimeEnrichmentFixture(t)
	physical := filepath.Join(filepath.Dir(c.Home), "physical-agent")
	if err := os.Rename(s.Root, physical); err != nil {
		t.Fatal(err)
	}
	c.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: s.Root, Path: physical}}
	return c, s, physical
}

func TestRuntimeAliasEnrichmentPhysicalBundle(t *testing.T) {
	c, spec, physical := aliasEnrichmentFixture(t)
	project := filepath.Join(filepath.Dir(c.Home), "project")
	spec.CWD = project
	for path, body := range map[string]string{
		filepath.Join(physical, "CLAUDE.md"):     "trusted physical harness",
		filepath.Join(physical, "settings.json"): `{"env":{"API_TOKEN":"synthetic-private-token"},"theme":"dark"}`,
		filepath.Join(project, "CLAUDE.md"):      "trusted CWD project",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec.Source.EpochID = "accepted-source-epoch"
	spec.Source.Revision = 7
	spec.Source.SHA256 = strings.Repeat("a", 64)
	before := spec
	observed, err := runtimeEnrichmentObservedRoot(c, spec)
	if err != nil || observed != physical {
		t.Fatalf("physical root = %q, %v", observed, err)
	}
	artifact, err := runtimeEnrichment(c)(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(artifact.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := artifact.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != artifact.ContentHash {
		t.Fatal("full bundle content hash mismatch")
	}
	if artifact.Complete || !slices.Contains(artifact.Missing, "native-launch-context-missing") {
		t.Fatal("missing launch context falsely complete")
	}
	if spec != before {
		t.Fatal("logical identity/source coordinate changed")
	}
	foundPhysical, foundProject := false, false
	for _, asset := range artifact.Assets {
		foundPhysical = foundPhysical || asset.Path == filepath.Join(physical, "CLAUDE.md")
		foundProject = foundProject || asset.Path == filepath.Join(project, "CLAUDE.md")
		if err := runtimeEnrichmentAssetPolicy(c)(t.Context(), spec, asset, true); err != nil {
			t.Fatal(err)
		}
	}
	if !foundPhysical || !foundProject {
		t.Fatalf("missing actual physical/CWD evidence: physical=%v project=%v", foundPhysical, foundProject)
	}
	credential := filepath.Join(physical, ".credentials.json")
	if err := runtimeEnrichmentAssetPolicy(c)(t.Context(), spec, sessioncapture.EnrichmentAsset{Path: credential, Scope: "user", Selector: "user:credentials", Kind: "file"}, true); err == nil {
		t.Fatal("known credential admitted")
	}
	if err := os.RemoveAll(physical); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	restored, err := snapshot.ReadBundle(t.Context(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	harness, cwd := false, false
	for hash, blob := range restored.Blobs {
		if fmt.Sprintf("%x", sha256.Sum256(blob)) != hash {
			t.Fatal("blob hash mismatch")
		}
		harness = harness || string(blob) == "trusted physical harness"
		cwd = cwd || string(blob) == "trusted CWD project"
		if strings.Contains(string(blob), "synthetic-private-token") {
			t.Fatal("credential escaped scrub")
		}
	}
	if !harness || !cwd {
		t.Fatal("bundle requires provider originals")
	}
}

func TestRuntimeAliasEnrichmentRootAuthority(t *testing.T) {
	t.Run("invalid coordinates never fallback", func(t *testing.T) {
		c, s, _ := aliasEnrichmentFixture(t)
		badRoot, badAgent := s, s
		badRoot.Root = filepath.Dir(s.Root)
		badAgent.Identity.Agent = "codex"
		for _, bad := range []sessioncapture.EnrichmentSpec{badRoot, badAgent} {
			if root, err := runtimeEnrichmentObservedRoot(c, bad); err == nil || root != "" {
				t.Fatal("invalid coordinate fell back")
			}
			if _, err := runtimeAliasEnrichmentExcluded(c, bad, nil); err == nil {
				t.Fatal("invalid coordinate accepted by exclusion lookup")
			}
		}
	})
	t.Run("replaced route and safe fallback", func(t *testing.T) {
		c, s, physical := aliasEnrichmentFixture(t)
		if err := os.RemoveAll(physical); err != nil {
			t.Fatal(err)
		}
		undeclared := filepath.Join(filepath.Dir(c.Home), "undeclared")
		if err := os.MkdirAll(undeclared, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(undeclared, physical); err != nil {
			t.Fatal(err)
		}
		if root, err := runtimeEnrichmentObservedRoot(c, s); err == nil || root != "" {
			t.Fatal("undeclared symlink selected")
		}
		safe := filepath.Join(filepath.Dir(c.Home), "safe")
		if err := os.MkdirAll(safe, 0o700); err != nil {
			t.Fatal(err)
		}
		c.RootAliases = append(c.RootAliases, capturedaemon.RootAlias{Agent: "claude", Root: s.Root, Path: safe})
		if root, err := runtimeEnrichmentObservedRoot(c, s); err != nil || root != safe {
			t.Fatalf("safe fallback %q: %v", root, err)
		}
	})
	t.Run("unavailable and leaf authority", func(t *testing.T) {
		c, s, physical := aliasEnrichmentFixture(t)
		if err := os.RemoveAll(physical); err != nil {
			t.Fatal(err)
		}
		if _, err := runtimeEnrichment(c)(t.Context(), s); err == nil {
			t.Fatal("unavailable directory reported success")
		}
		runtimeWrite(t, physical)
		if _, err := runtimeEnrichmentObservedRoot(c, s); err == nil {
			t.Fatal("regular file accepted as enrichment directory")
		}
		c.RootAliases = nil
		leaf := filepath.Join(filepath.Dir(c.Home), "exact.jsonl")
		runtimeWrite(t, leaf)
		c.LeafAliases = []capturedaemon.LeafAlias{{Agent: "claude", LogicalPath: filepath.Join(s.Root, "projects", "slug", "a.jsonl"), Path: leaf}}
		if _, err := runtimeEnrichmentObservedRoot(c, s); err == nil {
			t.Fatal("leaf granted parent directory")
		}
	})
}

func TestRuntimeAliasEnrichmentExclusionScope(t *testing.T) {
	for _, scope := range []string{"associated-session", "associated-root", "other-root", "other-session"} {
		t.Run(scope, func(t *testing.T) {
			c, s, current := aliasEnrichmentFixture(t)
			old := filepath.Join(filepath.Dir(c.Home), "old-agent")
			c.RootAliases = append(c.RootAliases, capturedaemon.RootAlias{Agent: "claude", Root: s.Root, Path: old})
			root, key := old, s.AdapterSessionKey
			if scope == "other-root" {
				root = filepath.Join(filepath.Dir(c.Home), "unrelated")
			}
			if scope == "other-session" {
				key = "different-session"
			}
			if scope == "associated-root" {
				key = ""
			}
			registry := exclusion.Registry{{Agent: "claude", Root: root, ResolvedRoot: root, SessionKey: key}}
			excluded, err := runtimeAliasEnrichmentExcluded(c, s, registry)
			// Shared root-level enrichment is withheld for any opt-out in
			// that root, not just the current session. UUID matching alone
			// must never extend this denial to an unrelated root.
			want := scope != "other-root"
			if err != nil || excluded != want {
				t.Fatalf("excluded=%v want=%v: %v", excluded, want, err)
			}
			if scope == "associated-session" {
				// Persist a real old-root opt-out, with no surviving old source directory.
				sf := adapters.SessionFile{Agent: "claude", Root: old, SessionKey: s.AdapterSessionKey, Path: filepath.Join(old, "projects", "slug", "12345678-1234-1234-1234-123456789abc.jsonl")}
				if _, err := exclusion.RecordFile(c.PolicyDir, sf); err != nil {
					t.Fatal(err)
				}
				asset := sessioncapture.EnrichmentAsset{Path: filepath.Join(current, "CLAUDE.md"), Scope: "user", Selector: "user:CLAUDE.md", Kind: "file"}
				if err := runtimeEnrichmentAssetPolicy(c)(t.Context(), s, asset, true); err == nil {
					t.Fatal("old absent-root opt-out bypassed by current route")
				}
			}
		})
	}
	c, s, _ := aliasEnrichmentFixture(t)
	c.RootAliases = nil
	paths, err := runtimeEnrichmentRootPaths(c, s)
	if err != nil || len(paths) != 1 || paths[0] != s.Root {
		t.Fatal("no-alias root semantics changed")
	}
	root, err := runtimeEnrichmentObservedRoot(c, s)
	if err != nil || root != s.Root {
		t.Fatal("no-alias missing-root semantics changed")
	}
	if denied, err := runtimeAliasEnrichmentExcluded(c, s, nil); err != nil || denied {
		t.Fatal("no-alias nil registry changed")
	}
}

func TestRuntimeAliasEnrichmentMissingContextRemainsPartial(t *testing.T) {
	c, s, physical := aliasEnrichmentFixture(t)
	path := filepath.Join(physical, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("synthetic harness without launch or CWD"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := runtimeEnrichment(c)(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if err := artifact.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if artifact.Complete || !slices.Contains(artifact.Missing, "native-launch-context-missing") || !slices.Contains(artifact.Missing, "project-context-missing") {
		t.Fatal("missing launch/CWD incorrectly reported complete")
	}
}
