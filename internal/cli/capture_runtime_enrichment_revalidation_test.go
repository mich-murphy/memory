package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestRuntimeEnrichmentAssetPolicyRevalidatesMutations(t *testing.T) {
	for _, kind := range []string{"source_credential_repoint", "protected_state_repoint", "protected_config_repoint", "live_exclusion", "cyclic_resolution"} {
		t.Run(kind, func(t *testing.T) {
			c, s := runtimeEnrichmentFixture(t)
			dir := filepath.Join(c.Home, "safe-project")
			if os.MkdirAll(dir, 0o700) != nil {
				t.Fatal("setup")
			}
			target := filepath.Join(dir, "artifact.txt")
			if os.WriteFile(target, []byte("synthetic"), 0o600) != nil {
				t.Fatal("setup")
			}
			path := target
			if kind == "source_credential_repoint" || kind == "cyclic_resolution" {
				path = filepath.Join(c.Home, "asset-link.txt")
				if os.Symlink(target, path) != nil {
					t.Fatal("setup")
				}
			}
			if kind == "protected_state_repoint" {
				old := filepath.Join(c.Home, "old-state")
				if os.Mkdir(old, 0o700) != nil {
					t.Fatal("setup")
				}
				c.Spool = filepath.Join(c.Home, "state-link")
				if os.Symlink(old, c.Spool) != nil {
					t.Fatal("setup")
				}
			}
			a := sessioncapture.EnrichmentAsset{Path: path, Identities: []string{path, target}, Scope: "project", Selector: "project:synthetic", Kind: "file"}
			check := runtimeEnrichmentAssetPolicy(c)
			if check(t.Context(), s, a, true) != nil {
				t.Fatal("initial allowed asset rejected")
			}
			switch kind {
			case "source_credential_repoint":
				cred := filepath.Join(dir, "auth.json.bak")
				if os.WriteFile(cred, []byte("synthetic"), 0o600) != nil || os.Remove(path) != nil || os.Symlink(cred, path) != nil {
					t.Fatal("mutate")
				}
			case "protected_state_repoint":
				if os.Remove(c.Spool) != nil || os.Symlink(dir, c.Spool) != nil {
					t.Fatal("mutate")
				}
			case "protected_config_repoint":
				if os.Remove(c.ConfigPath()) != nil || os.Symlink(target, c.ConfigPath()) != nil {
					t.Fatal("mutate")
				}
			case "live_exclusion":
				if _, err := exclusion.RecordFile(c.PolicyDir, adapters.SessionFile{Agent: s.Identity.Agent, Root: s.Root, SessionKey: s.AdapterSessionKey, Path: target}); err != nil {
					t.Fatal("mutate")
				}
			case "cyclic_resolution":
				if os.Remove(path) != nil || os.Symlink(path, path) != nil {
					t.Fatal("mutate")
				}
			}
			for _, delivery := range []bool{false, true} {
				if check(t.Context(), s, a, delivery) == nil {
					t.Fatal("mutation allowed")
				}
			}
		})
	}
}
