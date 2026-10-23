package sessioncapture

import (
	"context"
	"errors"
	"os"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/safefile"
)

// authorizedSource is private read authority, never supplied by logical metadata.
// before is sampled before policy callbacks; opening rejects aliases in every
// canonical component and replacements of the authorized file itself.
type authorizedSource struct {
	capturepolicy.Resolution
	before os.FileInfo
}

func (s authorizedSource) open(ctx context.Context) (*os.File, error) {
	if s.before != nil && !s.before.Mode().IsRegular() {
		return nil, errors.New("sessioncapture: regular source required")
	}
	return safefile.OpenResolved(ctx, s.Path, s.before)
}

// observePrivate is only for queue-owned immutable prepared/enrichment output.
// Original sources must instead pass checkObserved before any bytes are read.
func observePrivate(path string) (authorizedSource, error) {
	r, err := capturepolicy.Resolve(path)
	if err != nil {
		return authorizedSource{}, err
	}
	st, err := os.Lstat(r.Path)
	return authorizedSource{Resolution: r, before: st}, err
}
