package syncer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
)

// groupAliases fixes authority before preparation or submission. Different keys remain
// parallel, but one logical key has exactly one lifecycle, independent of response order.
func (s *Syncer) groupAliases(ctx context.Context, files []adapters.SessionFile) ([]adapters.SessionFile, error) {
	groups := map[string][]adapters.SessionFile{}
	for _, sf := range files {
		if !s.excludedFile(sf) {
			key := sf.Agent + "\x00" + sf.Key
			groups[key] = append(groups[key], sf)
		} else {
			s.Summary.Skipped++
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var out []adapters.SessionFile
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates := groups[key]
		slices.SortFunc(candidates, func(a, b adapters.SessionFile) int { return strings.Compare(a.Path, b.Path) })
		candidates = slices.CompactFunc(candidates, func(a, b adapters.SessionFile) bool { return a.Path == b.Path })
		chosen := candidates[0]
		if len(candidates) > 1 {
			var err error
			chosen, err = s.chooseAlias(ctx, candidates)
			if err != nil {
				s.fail(chosen.Path, err)
				continue
			}
		}
		sources := make([]raw.Source, 0, len(candidates))
		for _, sf := range candidates {
			encoding := "plain"
			if sf.Zstd {
				encoding = "zstd"
			}
			sources = append(sources, raw.Source{Path: sf.Path, SourcePath: sf.SourcePath, Encoding: encoding})
		}
		s.aliases[s.rawKey(chosen.Agent, chosen.Key)] = sources
		out = append(out, chosen)
	}
	return out, nil
}

type aliasCapture struct {
	sf   adapters.SessionFile
	path string
	size int64
	sha  string
}

func (s *Syncer) captureAlias(ctx context.Context, sf adapters.SessionFile) (aliasCapture, error) {
	resolution, err := capturepolicy.Resolve(sf.Path)
	if err != nil {
		return aliasCapture{}, err
	}
	path, err := s.stageDecoded(ctx, sf)
	if err != nil {
		return aliasCapture{}, err
	}
	result := aliasCapture{sf: sf, path: path}
	// Compare scrubbed bytes for every intermediate identity, independent of collecting agent.
	if slices.ContainsFunc(resolution.Identities, scrub.Recognizes) {
		body, err := readFile(sf)
		if err != nil {
			_ = os.Remove(path)
			return result, err
		} // disposable staged alias
		if err := os.WriteFile(path, body, 0o600); err != nil {
			_ = os.Remove(path)
			return result, err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return result, err
	}
	result.size = info.Size()
	result.sha, err = hashRange(path, 0, result.size)
	if err != nil {
		_ = os.Remove(path)
	} // never publish failed alias captures
	return result, err
}

func (s *Syncer) chooseAlias(ctx context.Context, candidates []adapters.SessionFile) (adapters.SessionFile, error) {
	chosen := candidates[0]
	captures := make([]aliasCapture, 0, len(candidates))
	defer func() {
		for i := range captures {
			capture := &captures[i]
			_ = os.Remove(capture.path)
		}
	}() // temporary comparison only
	for _, sf := range candidates {
		if sf.SessionKey != chosen.SessionKey || sf.Role != chosen.Role || resolvedPath(sf.Root) != resolvedPath(chosen.Root) {
			return chosen, errors.New("archive alias: conflicting logical identities")
		}
		capture, err := s.captureAlias(ctx, sf)
		if err != nil {
			return chosen, err
		}
		captures = append(captures, capture)
	}
	// Stable path ordering breaks ties only for equal decoded bytes, never divergence.
	longest := &captures[0]
	for i := range captures {
		capture := &captures[i]
		if capture.size > longest.size {
			longest = capture
		}
	}
	for i := range captures {
		capture := &captures[i]
		sha := longest.sha
		if capture.size != longest.size {
			var err error
			sha, err = hashRange(longest.path, 0, capture.size)
			if err != nil {
				return chosen, err
			}
		}
		if sha != capture.sha {
			return chosen, fmt.Errorf("archive alias: divergent contents for %s", chosen.Key)
		}
	}
	return longest.sf, nil
}
