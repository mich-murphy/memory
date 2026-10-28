package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// oracleCheckRuntimeEnrichmentAsset is the pre-change per-path algorithm, kept as the test oracle.
func oracleCheckRuntimeEnrichmentAsset(c capturedaemon.Config, spec sessioncapture.EnrichmentSpec, asset sessioncapture.EnrichmentAsset, delivery bool) error {
	if err := validateRuntimeEnrichment(c, spec); err != nil {
		return err
	}
	if !runtimeAssetMetadata(asset) {
		return errors.New("enrichment selection evidence unavailable")
	}
	registry, err := exclusion.Load(c.PolicyDir)
	if err != nil {
		return errors.New("enrichment exclusions unavailable")
	}
	aliasExcluded, err := runtimeAliasEnrichmentExcluded(c, spec, registry)
	if err != nil {
		return err
	}
	if aliasExcluded || registry.Excludes(adapters.SessionFile{Agent: spec.Identity.Agent, Root: spec.Root, SessionKey: spec.AdapterSessionKey, Path: asset.Path}) || registry.Excludes(adapters.SessionFile{Agent: spec.Identity.Agent, Root: spec.Root}) {
		return errors.New("enrichment excluded by effective policy")
	}
	paths := append([]string{asset.Path}, asset.Identities...)
	resolved, resolveErr := capturepolicy.Resolve(asset.Path)
	if resolveErr != nil && !delivery {
		return errors.New("enrichment source resolution unavailable")
	}
	paths = append(paths, resolved.Identities...)
	for _, path := range paths {
		if !runtimeAssetRoot(c, spec, path) {
			return errors.New("enrichment asset outside declared inventory")
		}
		scrubbed := scrub.Recognizes(path) || asset.Kind == "generated-subset"
		if err := runtimeFilesystemPolicy(c, path, scrubbed); err != nil {
			return err
		}
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func assetPathsFixture(t *testing.T) (capturedaemon.Config, sessioncapture.EnrichmentSpec, map[string]sessioncapture.EnrichmentAsset) {
	t.Helper()
	c, spec := runtimeEnrichmentFixture(t)
	write := func(p, body string) string {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	link := func(target, name string) string {
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
		return name
	}
	plain := write(filepath.Join(c.Home, ".claude", "CLAUDE.md"), "x")
	cred := write(filepath.Join(c.Home, ".ssh", "id_rsa"), "secret")
	settings := write(filepath.Join(c.Home, ".claude", "settings.json"), "{}")
	claudeJSON := write(filepath.Join(c.Home, ".claude.json"), "{}")
	outside := write(filepath.Join(filepath.Dir(c.Home), "undeclared", "CLAUDE.md"), "x")
	okLink := link(plain, filepath.Join(c.Home, ".claude", "link.md"))
	credLink := link(cred, filepath.Join(c.Home, ".claude", "credlink.md"))
	cfgLink := link(c.ConfigPath(), filepath.Join(c.Home, ".claude", "cfglink.md"))
	mk := func(kind, path string, ids ...string) sessioncapture.EnrichmentAsset {
		return sessioncapture.EnrichmentAsset{Path: path, Identities: ids, Scope: "project", Selector: "project:" + path, Kind: kind}
	}
	return c, spec, map[string]sessioncapture.EnrichmentAsset{
		"plain":                 mk("file", plain, plain),
		"no identities":         mk("file", plain),
		"symlink":               mk("file", okLink, okLink, plain),
		"duplicate identities":  mk("file", plain, plain, plain, plain),
		"outside second":        mk("file", plain, plain, outside),
		"outside first":         mk("file", outside, outside),
		"credential":            mk("file", cred, cred),
		"credential second":     mk("file", plain, plain, cred),
		"credential symlink":    mk("file", credLink, credLink),
		"config symlink":        mk("file", cfgLink, cfgLink),
		"config":                mk("file", c.ConfigPath(), c.ConfigPath()),
		"spool":                 mk("file", filepath.Join(c.Spool, "payload"), filepath.Join(c.Spool, "payload")),
		"missing":               mk("file", filepath.Join(c.Home, ".claude", "missing.md"), filepath.Join(c.Home, ".claude", "missing.md")),
		"generated-subset":      mk("generated-subset", settings, settings, settings),
		"scrubbed claude.json":  mk("file", claudeJSON, claudeJSON),
		"generated claude.json": mk("generated-subset", claudeJSON, claudeJSON),
		"bad metadata":          {Path: "relative", Kind: "file"},
	}
}

func TestCheckRuntimeEnrichmentAssetEquivalentToPerPathOracle(t *testing.T) {
	c, spec, assets := assetPathsFixture(t)
	for name, asset := range assets {
		for _, delivery := range []bool{true, false} {
			got := checkRuntimeEnrichmentAsset(t.Context(), c, spec, asset, delivery)
			want := oracleCheckRuntimeEnrichmentAsset(c, spec, asset, delivery)
			if errString(got) != errString(want) {
				t.Errorf("%s delivery=%v: got %q want %q", name, delivery, errString(got), errString(want))
			}
		}
	}
}

func TestCheckRuntimeEnrichmentAssetBuildsPolicyOnceAndChecksDistinctPathsOnce(t *testing.T) {
	c, spec, assets := assetPathsFixture(t)
	asset := assets["symlink"]
	asset.Identities = append(append([]string{}, asset.Identities...), asset.Path, asset.Identities[0])
	resolves := map[string]int{}
	built := 0
	err := checkRuntimeEnrichmentAssetWith(t.Context(), c, spec, asset, true,
		func(p string) (capturepolicy.Resolution, error) { resolves[p]++; return capturepolicy.Resolve(p) },
		func() (*capturepolicy.Policy, error) { built++; return newRuntimeFilesystemSet(c) })
	if err != nil {
		t.Fatal(err)
	}
	if built != 1 {
		t.Fatalf("policy built %d times", built)
	}
	// Each distinct path is resolved once by its policy check; asset.Path once more for the
	// call-level resolution.
	plain := assets["plain"].Path
	for _, p := range []string{asset.Path, plain} {
		want := 1
		if p == asset.Path {
			want = 2
		}
		if resolves[p] != want {
			t.Fatalf("%s resolved %d times, want %d (%v)", p, resolves[p], want, resolves)
		}
	}
}

func TestCheckRuntimeEnrichmentAssetPolicyErrorFailsClosedAndOnlyWhenReached(t *testing.T) {
	c, spec, assets := assetPathsFixture(t)
	built := 0
	failing := func() (*capturepolicy.Policy, error) { built++; return nil, errors.New("boom") }
	err := checkRuntimeEnrichmentAssetWith(t.Context(), c, spec, assets["plain"], true, capturepolicy.Resolve, failing)
	if errString(err) != "source excluded by filesystem policy" || built != 1 {
		t.Fatalf("got %v, built %d", err, built)
	}
	built = 0
	err = checkRuntimeEnrichmentAssetWith(t.Context(), c, spec, assets["outside first"], true, capturepolicy.Resolve, failing)
	if errString(err) != "enrichment asset outside declared inventory" || built != 0 {
		t.Fatalf("early failure built policy: %v, built %d", err, built)
	}
	err = checkRuntimeEnrichmentAssetWith(t.Context(), c, spec, assets["bad metadata"], true, capturepolicy.Resolve, failing)
	if built != 0 || err == nil {
		t.Fatalf("metadata failure built policy: %v", err)
	}
}
