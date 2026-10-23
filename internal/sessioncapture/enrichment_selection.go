package sessioncapture

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/publication"
)

// selectOptional retains full private evidence and redacts only the public view.
// Required sources remain subject to all existing history checks.
func (q *Queue) selectOptional(ctx context.Context, m publication.Manifest, es []evidence) (publication.Manifest, error) {
	if len(m.Sources) != len(es) {
		return m, ErrIntegrity
	}
	for i := range m.Sources {
		if !m.Sources[i].Optional {
			continue
		}
		src, err := q.optionalSource(ctx, m.Sources[i], &es[i])
		if err != nil {
			return m, err
		}
		m.Sources[i] = src
		if src.Withheld != nil {
			m.Coverage.Enrichment = "partial"
			if !slices.Contains(m.Coverage.Missing, publication.MissingSourceWithheld) {
				m.Coverage.Missing = append(m.Coverage.Missing, publication.MissingSourceWithheld)
			}
		}
	}

	return m, nil
}

func (q *Queue) optionalSource(ctx context.Context, src publication.Source, e *evidence) (publication.Source, error) {
	if src.Withheld != nil {
		if e.Original == nil {
			return src, ErrIntegrity
		}
		original := *e.Original
		if publication.SourceKeyHash(original) != publication.SourceKeyHash(src) || publication.ObjectCommitment(original.Object) != src.Withheld.SnapshotCommitment {
			return src, ErrIntegrity
		}
		src = original
	}
	original := src
	e.SelectionReason = ""
	e.Original = &original
	reason, err := q.optionalReason(ctx, src, *e)
	if err != nil {
		return src, err
	}
	if ctx.Err() != nil {
		return src, ctx.Err()
	}
	if reason != "" {
		e.SelectionReason = reason
		return publication.WithholdSource(src, reason)
	}
	return src, nil
}

func (q *Queue) optionalReason(ctx context.Context, src publication.Source, e evidence) (string, error) {
	if _, err := q.check(ctx, e, true); err != nil {
		if errors.Is(err, ErrSessionExcluded) {
			return "", err
		}
		return "policy", nil
	}
	var quota int
	if err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE session=? AND optional_repair_gap='quota'`, e.Request.Identity.BaseKey()).Scan(&quota); err != nil {
		return "", err
	}
	if quota != 0 {
		return "unavailable", nil
	}
	verifyErr := q.verify(ctx, src.Object)
	switch {
	case verifyErr == nil:
		blocked, err := q.unavailableLatch(ctx, e.Request.Identity, src)
		if err != nil {
			return "", err
		}
		if blocked {
			return "unavailable", nil
		}
		return "", nil
	case ctx.Err() != nil:
		return "", ctx.Err()
	default:
		return "integrity", nil
	}
}

func capturedSpec(m publication.Manifest, es []evidence, changedSource string) (EnrichmentSpec, bool) {
	for i := range m.Sources {
		s := &m.Sources[i]
		if s.SourceID != changedSource || s.Optional || (s.Role != "main" && s.Role != "subagent" && s.Role != "transcript") || s.State != "active" {
			continue
		}
		root, adapter := requestCoordinates(es[i].Request)
		return EnrichmentSpec{Kind: "snapshot", Identity: m.Identity, BasisPublication: m.PublicationID, BasisSequence: m.Sequence, Source: SourceCoordinate{SourceID: s.SourceID, Path: es[i].Request.Path, EpochID: s.EpochID, Revision: s.Revision, SHA256: s.Object.SHA256}, Root: root, AdapterSessionKey: adapter, SelectionVersion: "snapshot/v1"}, true
	}
	return EnrichmentSpec{}, false
}

// carryOptionalSelection performs metadata-only work on the raw acceptance path.
// Policy/build/hash work belongs exclusively to enrichment/delivery operations.
func carryOptionalSelection(m publication.Manifest, es []evidence) (publication.Manifest, error) {
	if len(m.Sources) != len(es) {
		return m, ErrIntegrity
	}
	for i := range m.Sources {
		src := m.Sources[i]
		if !src.Optional {
			continue
		}
		if src.Withheld != nil {
			continue
		}
		original := src
		es[i].Original = &original
		if es[i].SelectionReason != "" {
			var err error
			m.Sources[i], err = publication.WithholdSource(src, es[i].SelectionReason)
			m.Coverage.Enrichment = "partial"
			if !slices.Contains(m.Coverage.Missing, publication.MissingSourceWithheld) {
				m.Coverage.Missing = append(m.Coverage.Missing, publication.MissingSourceWithheld)
			}
			if err != nil {
				return m, err
			}
		}
	}
	return m, nil
}

// SupersedeOptional creates a higher sequence safe view from the latest accepted
// state. Originals remain retained; superseded does not mean remotely published.
func (q *Queue) supersedeOptional(ctx context.Context, i publication.Identity) (bool, error) {
	return q.selectionView(ctx, i, true)
}

func (q *Queue) selectionView(ctx context.Context, i publication.Identity, force bool) (bool, error) {
	m, es, err := q.latest(ctx, i)
	if err != nil {
		return false, err
	}
	for n := range m.Sources {
		if !m.Sources[n].Optional {
			if _, err := q.check(ctx, es[n], true); err != nil {
				return false, err
			}
			if err := q.verify(ctx, m.Sources[n].Object); err != nil {
				return false, err
			}
		}
	}
	if err := q.recheckScopes(ctx, es); err != nil {
		return false, err
	}
	view := m
	view.Sources = slices.Clone(m.Sources)
	selected, err := q.selectOptional(ctx, view, es)
	if err != nil {
		return false, err
	}
	changed := false
	for n := range m.Sources {
		if (m.Sources[n].Withheld == nil) != (selected.Sources[n].Withheld == nil) {
			changed = true
		}
	}
	if !changed && !force {
		return false, nil
	}
	selected.Sequence++
	// Never fabricate new byte capture or observation timestamps for selection.
	// Selection views preserve capture timestamps but must not enqueue work.
	_, err = q.acceptView(ctx, selected, es, "", true)
	return err == nil, err
}

func optionalFailure(err error) bool {
	return errors.Is(err, ErrPolicy) || errors.Is(err, ErrIntegrity) || errors.Is(err, errOptionalUnavailable)
}

var errSelectionSuperseded = errors.New("sessioncapture: selection superseded")

func selectedEvidence(m publication.Manifest, es []evidence) []evidence {
	out := make([]evidence, 0, len(es))
	for i := range m.Sources {
		s := &m.Sources[i]
		if publication.IsSelected(*s) && i < len(es) {
			out = append(out, es[i])
		}
	}
	return out
}

func requestCoordinates(r Request) (root, adapter string) {
	for _, a := range r.Associations {
		if v, ok := strings.CutPrefix(a, "root:"); ok {
			root = v
		}
		if v, ok := strings.CutPrefix(a, "adapter-session:"); ok {
			adapter = v
		}
	}
	return root, adapter
}

// RefreshOptionalSelection reselects only retained, policy/integrity-checked bytes
// and preserves original capture times. It never restores an old raw view.
func (q *Queue) RefreshOptionalSelection(ctx context.Context, i publication.Identity) (bool, error) {
	unlock, err := q.lockSession(ctx, i)
	if err != nil {
		return false, err
	}
	defer unlock()
	return q.selectionView(ctx, i, false)
}

func (q *Queue) hasDeniedOptional(ctx context.Context, m publication.Manifest, es []evidence) bool {
	for i := range m.Sources {
		if m.Sources[i].Optional && publication.IsSelected(m.Sources[i]) {
			reason, err := q.optionalReason(ctx, m.Sources[i], es[i])
			if err == nil && reason != "" {
				return true
			}
		}
	}
	return false
}

// A whole-session opt-out is not an optional asset selection decision. Retained
// private identities remain authoritative even when every public source is redacted.
func (q *Queue) recheckScopes(ctx context.Context, es []evidence) error {
	for i := range es {
		e := &es[i]
		_, declared, err := q.requestCoordinates(e.Request)
		if err != nil {
			return err
		}
		requests := unionIdentities(declared, e.Identities)
		for _, path := range requests {
			r := e.Request
			r.Path = path
			err := q.opts.CheckPolicy(ctx, r)
			if errors.Is(err, ErrSessionExcluded) {
				return errors.Join(ErrPolicy, ErrSessionExcluded)
			}
			if ctx.Err() != nil && pureCancellation(err, ctx.Err()) {
				return ctx.Err()
			}
		}
	}
	return nil
}

// Rehydration is metadata-only, and validates commitments before private fields
// can participate in identity/epoch checks. It does not authorize reselection.
func rehydrateSource(public publication.Source, e evidence) (publication.Source, error) {
	if e.Original == nil {
		if public.Withheld != nil {
			return public, ErrIntegrity
		}
		return public, nil
	}
	original := *e.Original
	if original.EpochID != public.EpochID || original.Revision != public.Revision || original.Role != public.Role || !original.Optional || publication.SourceKeyHash(original) != publication.SourceKeyHash(public) {
		return public, ErrIntegrity
	}
	commitment := publication.ObjectCommitment(public.Object)
	if public.Withheld != nil {
		commitment = public.Withheld.SnapshotCommitment
	}
	if publication.ObjectCommitment(original.Object) != commitment {
		return public, ErrIntegrity
	}
	return original, nil
}

var errOptionalUnavailable = errors.New("sessioncapture: optional object awaits remote repair")
