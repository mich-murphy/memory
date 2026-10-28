package cli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/sessioncapture"
	"github.com/mich-murphy/memory/internal/snapshot"
)

func validateRuntimeEnrichment(c capturedaemon.Config, s sessioncapture.EnrichmentSpec) error {
	if !c.Enrichment || s.Identity.ArchiveID != c.ArchiveID || s.Identity.MachineID != c.MachineID || !sessioncapture.OwnershipSession(s.Root, s.Identity.SessionID, s.AdapterSessionKey) {
		return errors.New("enrichment scope unavailable")
	}
	if !slices.ContainsFunc(c.SourceRoots(), func(root capturedaemon.Root) bool {
		return root.Agent == s.Identity.Agent && root.Path == s.Root
	}) {
		return errors.New("enrichment outside configured inventory")
	}
	return runtimeUnresolvedNativePolicy(c, s.Identity.Agent)
}

func runtimeEnrichment(c capturedaemon.Config) sessioncapture.EnrichFunc {
	return func(ctx context.Context, spec sessioncapture.EnrichmentSpec) (sessioncapture.EnrichmentArtifact, error) {
		return buildRuntimeEnrichment(ctx, c, spec)
	}
}

func buildRuntimeEnrichment(ctx context.Context, c capturedaemon.Config, spec sessioncapture.EnrichmentSpec) (sessioncapture.EnrichmentArtifact, error) {
	var result sessioncapture.EnrichmentArtifact
	if err := validateRuntimeEnrichment(c, spec); err != nil {
		return result, err
	}
	a := adapters.All(c.Home)[spec.Identity.Agent]
	if a == nil {
		return result, errors.New("enrichment agent unavailable")
	}
	policy, err := capturepolicy.New(c.ConfigPath(), c.PolicyDir)
	if err != nil {
		return result, errors.New("enrichment policy unavailable")
	}
	registry, err := exclusion.Load(c.PolicyDir)
	if err != nil {
		return result, errors.New("enrichment exclusions unavailable")
	}
	check := runtimeEnrichmentAssetPolicy(c)
	observedRoot, err := runtimeEnrichmentObservedRoot(c, spec)
	if err != nil {
		return result, err
	}
	userBase := observedRoot
	if spec.Identity.Agent == "pi" && spec.Root != filepath.Join(c.Home, ".pi", "agent") && !strings.HasPrefix(spec.AdapterSessionKey, "roots/") {
		userBase = filepath.Join(c.Home, ".pi", "agent")
	}
	snap, err := snapshot.BuildWithOptions(ctx, a, observedRoot, spec.CWD, spec.AdapterSessionKey, snapshot.Options{
		Policy: policy, Exclusions: registry, ConfigPath: c.ConfigPath(), PolicyDir: c.PolicyDir,
		Home: c.Home, UserBase: userBase, WorkingDirectory: spec.WorkingDirectory,
		Authorize: func(ctx context.Context, asset snapshot.AssetEvidence) (snapshot.AssetEvidence, error) {
			return asset, check(ctx, spec, runtimeSnapshotAsset(asset), false)
		},
	})
	if err != nil {
		return result, err
	}
	bundle, err := snapshot.Bundle(ctx, snap)
	if err != nil {
		return result, err
	}
	result.Body, result.ContentHash, result.Complete = bundle.Reader, bundle.SHA256, snap.Complete
	for _, asset := range bundle.Evidence {
		result.Assets = append(result.Assets, runtimeSnapshotAsset(asset))
	}
	if !snap.Complete {
		result.Missing = append(result.Missing, "snapshot-selection-partial")
	}
	if spec.LaunchID == "" {
		result.Complete = false
		result.Missing = append(result.Missing, "native-launch-context-missing")
	}
	if spec.CWD == "" {
		result.Complete = false
		result.Missing = append(result.Missing, "project-context-missing")
	}
	return result, nil
}

func runtimeSnapshotAsset(asset snapshot.AssetEvidence) sessioncapture.EnrichmentAsset {
	return sessioncapture.EnrichmentAsset{Path: asset.OriginalPath, Identities: slices.Clone(asset.Identities), Scope: asset.Scope, Selector: asset.Selector, Kind: asset.InputKind}
}

// This callback is reachable only through the trusted snapshot selector or its
// private persisted evidence. Extra roots authorize those declared routes, never
// ordinary discovery or arbitrary capture requests outside the source inventory.
func runtimeEnrichmentAssetPolicy(c capturedaemon.Config) sessioncapture.CheckEnrichmentAssetFunc {
	return func(ctx context.Context, spec sessioncapture.EnrichmentSpec, asset sessioncapture.EnrichmentAsset, delivery bool) error {
		return checkRuntimeEnrichmentAsset(ctx, c, spec, asset, delivery)
	}
}

func checkRuntimeEnrichmentAsset(ctx context.Context, c capturedaemon.Config, spec sessioncapture.EnrichmentSpec, asset sessioncapture.EnrichmentAsset, delivery bool) error {
	return checkRuntimeEnrichmentAssetWith(ctx, c, spec, asset, delivery, capturepolicy.Resolve, func() (*capturepolicy.Policy, error) {
		return newRuntimeFilesystemSet(c)
	})
}

// checkRuntimeEnrichmentAssetWith checks each distinct path once and builds the
// filesystem policy lazily, at most once, for this call only. Lazy construction
// keeps errors where they were: a call failing at runtimeAssetRoot or at a path's
// resolution never builds (or reports an error from) the policy.
func checkRuntimeEnrichmentAssetWith(ctx context.Context, c capturedaemon.Config, spec sessioncapture.EnrichmentSpec, asset sessioncapture.EnrichmentAsset, delivery bool, resolve func(string) (capturepolicy.Resolution, error), newPolicy func() (*capturepolicy.Policy, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	resolved, resolveErr := resolve(asset.Path)
	if resolveErr != nil && !delivery {
		return errors.New("enrichment source resolution unavailable")
	}
	paths = append(paths, resolved.Identities...)
	var (
		policy    *capturepolicy.Policy
		policyErr error
		built     bool
	)
	lazyPolicy := func() (*capturepolicy.Policy, error) {
		if !built {
			built = true
			policy, policyErr = newPolicy()
		}
		return policy, policyErr
	}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if !runtimeAssetRoot(c, spec, path) {
			return errors.New("enrichment asset outside declared inventory")
		}
		scrubbed := scrub.Recognizes(path) || asset.Kind == "generated-subset"
		if err := runtimeFilesystemPolicyWith(path, scrubbed, resolve, lazyPolicy); err != nil {
			return err
		}
	}
	return nil
}

func runtimeAssetMetadata(asset sessioncapture.EnrichmentAsset) bool {
	return filepath.IsAbs(asset.Path) && filepath.Clean(asset.Path) == asset.Path && asset.Scope != "" && asset.Selector != "" && !strings.ContainsAny(asset.Scope+asset.Selector, "\x00\r\n") &&
		slices.Contains([]string{"file", "generated-subset", "plugin-manifest", "package-provenance", "git-selection"}, asset.Kind)
}

func runtimeAssetRoot(c capturedaemon.Config, spec sessioncapture.EnrichmentSpec, path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	rootPaths, err := runtimeEnrichmentRootPaths(c, spec)
	if err != nil {
		return false
	}
	roots := append([]string{c.Home}, rootPaths...)
	roots = append(roots, c.EnrichmentRoots...)
	if spec.CWD != "" && filepath.IsAbs(spec.CWD) {
		roots = append(roots, filepath.Clean(spec.CWD))
	}
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
