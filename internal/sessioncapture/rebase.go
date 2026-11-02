package sessioncapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mich-murphy/memory/internal/publication"
)

// rebaseManifest merges a local accepted view into the verified remote current so a
// rebuilt or lagging spool converges instead of conflicting forever. Every remote
// source epoch is retained (an active remote epoch superseded by a new local epoch of
// the same source is sealed, as an ordinary capture would), the local epochs are added
// or advanced, and the result sits at remote.Sequence+1. origin[i] is the index into
// local.Sources that merged.Sources[i] came from, or -1 for a remote-only source.
// covered reports that the remote already holds everything local contributes.
//
// Divergence that cannot be reconciled is publication.ErrConflict: the same source
// epoch at the same revision with different bytes can only come from a writer whose
// history is not a prefix of ours. A merge that cannot fit the manifest limits is
// ErrQuota (typed, visible, deterministic). Nothing is ever overwritten.
func rebaseManifest(remote, local publication.Manifest) (merged publication.Manifest, origin []int, covered bool, err error) {
	type key [2]string
	at := map[key]int{}
	out := slices.Clone(remote.Sources)
	origin = make([]int, len(out))
	for i := range out {
		origin[i] = -1
		at[key{publication.SourceKeyHash(out[i]), out[i].EpochID}] = i
	}
	contributed := 0
	for j := range local.Sources {
		s := local.Sources[j]
		k := key{publication.SourceKeyHash(s), s.EpochID}
		i, ok := at[k]
		if !ok {
			at[k] = len(out)
			out = append(out, s)
			origin = append(origin, j)
			contributed++
			continue
		}
		r := remote.Sources[i]
		switch {
		case s.Revision < r.Revision:
			// Same epoch, so the remote revision extends ours as a proven append.
		case s.Revision == r.Revision:
			if publication.ObjectCommitment(s.Object) != publication.ObjectCommitment(r.Object) || (s.Withheld != nil) != (r.Withheld != nil) {
				return merged, nil, false, fmt.Errorf("%w: remote and local disagree on one source epoch revision", publication.ErrConflict)
			}
		default:
			out[i], origin[i] = s, j
			contributed++
		}
	}
	if contributed == 0 {
		return merged, nil, true, nil
	}
	sealSuperseded(out, len(remote.Sources))
	if len(out) > publication.MaxSources {
		return merged, nil, false, fmt.Errorf("%w: rebased manifest has %d sources", ErrQuota, len(out))
	}
	merged = local
	merged.Sequence = remote.Sequence + 1
	merged.PublishedAtNS = 0
	merged.Sources = out
	merged.Coverage = rebaseCoverage(remote.Coverage, local.Coverage, out)
	merged.PublicationID = ""
	b, err := json.Marshal(merged)
	if err != nil {
		return merged, nil, false, err
	}
	if len(b) > publication.MaxMetadata {
		return merged, nil, false, fmt.Errorf("%w: rebased manifest metadata", ErrQuota)
	}
	merged.PublicationID = digest(b)
	return merged, origin, false, nil
}

// sealSuperseded seals each remote active epoch that another active epoch of the same
// source has replaced, as an ordinary capture would.
func sealSuperseded(out []publication.Source, remoteCount int) {
	for i := range remoteCount {
		if out[i].State != "active" {
			continue
		}
		for j := range out {
			if j != i && out[j].State == "active" && publication.SourceKeyHash(out[j]) == publication.SourceKeyHash(out[i]) && out[j].EpochID != out[i].EpochID {
				out[i].State = "sealed"
				break
			}
		}
	}
}

func rebaseCoverage(remote, local publication.Coverage, sources []publication.Source) publication.Coverage {
	c := local
	c.Missing = slices.Clone(local.Missing)
	if remote.Discovery != local.Discovery {
		c.Discovery = "partial"
	}
	if remote.Enrichment != local.Enrichment {
		c.Enrichment = "partial"
	}
	for _, m := range remote.Missing {
		c.Missing = appendUniqueMissing(c.Missing, m)
	}
	if slices.ContainsFunc(sources, func(s publication.Source) bool { return !publication.IsSelected(s) }) {
		c.Enrichment = "partial"
		c.Missing = appendUniqueMissing(c.Missing, publication.MissingSourceWithheld)
	}
	return c
}

// deliverRebased publishes m above the verified remote current. cause is returned when
// there is no remote current to rebase on.
func (q *Queue) deliverRebased(ctx context.Context, b publication.Backend, m publication.Manifest, es []evidence, cause error) error {
	body, token, err := b.Current(ctx, m.Identity.CurrentKey())
	if errors.Is(err, publication.ErrNotFound) {
		return cause
	}
	if err != nil {
		return err
	}
	var c publication.Current
	if err := json.Unmarshal(body, &c); err != nil {
		return fmt.Errorf("%w: current", ErrIntegrity)
	}
	remote, err := publication.Read(ctx, b, m.Identity)
	if err != nil {
		return err
	}
	if remote.PublicationID != c.PublicationID || remote.Sequence != c.Sequence {
		return publication.ErrConflict // moved while being verified; retry reads the new current
	}
	merged, origin, covered, err := rebaseManifest(remote, m)
	if err != nil || covered {
		return err
	}
	merged.Identity = m.Identity
	all := rebasedEvidence(merged, origin, es)
	// Remote-only sources have no local bytes or policy identities, only their
	// session identity, source id, role and publication path. That is still what
	// the exclusion registry matches on, so recheck every merged source and fail
	// closed (ErrPolicy+ErrSessionExcluded) before anything is uploaded or published.
	if err := q.recheckScopes(ctx, all); err != nil {
		return err
	}
	trusted := publication.TrustedObjects(ctx, b, merged)
	for j := range merged.Sources {
		if origin[j] < 0 || !publication.IsSelected(merged.Sources[j]) {
			continue
		}
		if err := q.uploadOne(ctx, b, all, j, &merged.Sources[j], trusted[merged.Sources[j].Object]); err != nil {
			return err
		}
	}
	if err := q.recheck(ctx, selectedEvidence(m, es)); err != nil {
		return err
	}
	if err := q.fault("manifest-before"); err != nil {
		return err
	}
	fb := faultBackend{Backend: b, q: q, evidence: selectedEvidence(m, es), scopeEvidence: all, manifest: m}
	if _, err := publication.Publish(ctx, fb, merged, token); err != nil {
		return err
	}
	return q.fault("current-published")
}

// rebasedEvidence pairs each merged source with its local evidence; remote-only
// sources get identity-only evidence, which is never uploaded or re-read locally.
func rebasedEvidence(merged publication.Manifest, origin []int, es []evidence) []evidence {
	all := make([]evidence, len(merged.Sources))
	for i := range merged.Sources {
		if origin[i] >= 0 {
			all[i] = es[origin[i]]
			continue
		}
		s := merged.Sources[i]
		all[i] = evidence{EpochID: s.EpochID, Request: Request{Identity: merged.Identity, SourceID: s.SourceID, Role: s.Role, Optional: s.Optional, Path: s.Path}}
	}
	return all
}
