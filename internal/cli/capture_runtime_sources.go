package cli

import (
	"context"

	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func (r runtimeComposition) queueOptions() sessioncapture.Options {
	c := r.config
	opts := sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, MinFreeBytes: c.MinFreeBytes, MaxInventoryRecords: c.MaxInventoryRecords, CheckPolicy: r.policy(false), EnableEnrichment: c.Enrichment}
	if runtimeHasAliases(c) {
		opts.Coordinates = r.inventory
	}
	return opts
}

// Periodic discovery and transient native hooks share one declared inventory and
// all-identity metadata policy. Neither request-supplied associations nor a native
// session string grant coordinate authority.
func (r runtimeComposition) sourceOptions(configPath string) capturesource.Options {
	return r.sourceOptionsWith(configPath, r.policy(true))
}

func (r runtimeComposition) sourceOptionsWith(configPath string, policy func(context.Context, sessioncapture.Request) error) capturesource.Options {
	c := r.config
	roots := make([]capturesource.Root, len(c.Roots))
	for i, r := range c.Roots {
		roots[i] = capturesource.Root{Agent: r.Agent, Path: r.Path}
	}
	opts := capturesource.Options{
		ArchiveID: c.ArchiveID, MachineID: c.MachineID, Home: c.Home, Roots: roots, OnlyRoots: !c.DefaultRoots, Derived: true, ConfigPath: configPath, StateDir: c.Spool, EntriesPerRoot: c.EntriesPerRoot,
		LookupOwner: func(ctx context.Context, root capturesource.Root, uuid string) (string, string, error) {
			return sessioncapture.ReadOwnership(ctx, c.Spool, c.ArchiveID, root.Agent, root.Path, uuid)
		},
		Policy: r.scopePolicy(policy),
	}
	if runtimeHasAliases(c) {
		opts.Coordinates = r.inventory
	}
	return opts
}

// scopePolicy adapts a request policy to a discovery scope: every identity of the
// scope, not just its path, must pass.
func (r runtimeComposition) scopePolicy(policy func(context.Context, sessioncapture.Request) error) func(context.Context, capturesource.Scope) error {
	c := r.config
	return func(ctx context.Context, s capturesource.Scope) error {
		paths := append([]string{s.Path}, s.Identities...)
		for _, path := range paths {
			if err := policy(ctx, sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: s.Root.Agent}, Path: path}); err != nil {
				return err
			}
		}
		return nil
	}
}
