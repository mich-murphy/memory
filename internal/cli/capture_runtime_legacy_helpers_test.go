package cli

import (
	"context"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Standalone fixture entry points deliberately construct a new snapshot per call;
// production callbacks share one composition for the entire runtime invocation.
func runtimePolicy(c capturedaemon.Config) func(context.Context, sessioncapture.Request) error {
	return runtimePolicyMode(c, false)
}

func runtimeDiscoveryPolicy(c capturedaemon.Config) func(context.Context, sessioncapture.Request) error {
	return runtimePolicyMode(c, true)
}

func runtimePolicyMode(c capturedaemon.Config, discovery bool) func(context.Context, sessioncapture.Request) error {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return func(context.Context, sessioncapture.Request) error { return err }
	}
	return r.policy(discovery)
}

func runtimePrivacyOwner(ctx context.Context, c capturedaemon.Config, req sessioncapture.Request, discovery bool) (*adapters.SessionFile, error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return nil, err
	}
	return r.privacyOwner(ctx, req, discovery)
}

func runtimeOwningRoot(c capturedaemon.Config, agent, path string) string {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return ""
	}
	return r.owningRoot(agent, path)
}

func runtimeQueueOptions(c capturedaemon.Config) (sessioncapture.Options, error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return sessioncapture.Options{}, err
	}
	return r.queueOptions(), nil
}

func runtimeSourceOptions(c capturedaemon.Config, path string) (capturesource.Options, error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return capturesource.Options{}, err
	}
	return r.sourceOptions(path), nil
}

func runtimeDiscovery(c capturedaemon.Config, path string) (func(context.Context) (sessioncapture.DiscoveryBatch, error), func() error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return func(context.Context) (sessioncapture.DiscoveryBatch, error) {
			return sessioncapture.DiscoveryBatch{}, err
		}, func() error { return nil }
	}
	return r.discovery(path)
}

func runtimeClaimedSessionExcluded(c capturedaemon.Config, req sessioncapture.Request, registry exclusion.Registry) (bool, error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return false, err
	}
	return r.claimedSessionExcluded(req, registry)
}

func runtimeEnrichment(c capturedaemon.Config) sessioncapture.EnrichFunc {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return func(context.Context, sessioncapture.EnrichmentSpec) (sessioncapture.EnrichmentArtifact, error) {
			return sessioncapture.EnrichmentArtifact{}, err
		}
	}
	return r.enrichment()
}

func runtimeEnrichmentAssetPolicy(c capturedaemon.Config) sessioncapture.CheckEnrichmentAssetFunc {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return func(context.Context, sessioncapture.EnrichmentSpec, sessioncapture.EnrichmentAsset, bool) error {
			return err
		}
	}
	return r.enrichmentAssetPolicy()
}

func runtimeEnrichmentRootPaths(c capturedaemon.Config, s sessioncapture.EnrichmentSpec) ([]string, error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return nil, err
	}
	return r.enrichmentRootPaths(s)
}

func runtimeEnrichmentObservedRoot(c capturedaemon.Config, s sessioncapture.EnrichmentSpec) (string, error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return "", err
	}
	return r.enrichmentObservedRoot(s)
}

func runtimeAliasEnrichmentExcluded(c capturedaemon.Config, s sessioncapture.EnrichmentSpec, registry exclusion.Registry) (bool, error) {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return false, err
	}
	return r.aliasEnrichmentExcluded(s, registry)
}

func checkRuntimeEnrichmentAsset(ctx context.Context, c capturedaemon.Config, s sessioncapture.EnrichmentSpec, a sessioncapture.EnrichmentAsset, d bool) error {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return err
	}
	return r.checkEnrichmentAsset(ctx, s, a, d)
}

func checkRuntimeEnrichmentAssetWith(ctx context.Context, c capturedaemon.Config, s sessioncapture.EnrichmentSpec, a sessioncapture.EnrichmentAsset, resolve func(string) (capturepolicy.Resolution, error), newPolicy func() (*capturepolicy.Policy, error)) error {
	r, err := newRuntimeComposition(c)
	if err != nil {
		return err
	}
	return r.checkEnrichmentAssetWith(ctx, s, a, true, resolve, newPolicy)
}

func runtimeAssetRoot(c capturedaemon.Config, s sessioncapture.EnrichmentSpec, path string) bool {
	r, err := newRuntimeComposition(c)
	return err == nil && r.assetRoot(s, path)
}
