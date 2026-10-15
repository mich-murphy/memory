package syncer

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
)

// deletionFences retains generations behind unobserved traversal boundaries. Paths
// stay logical: resolving a link would lose dangling links and historical aliases.
// Comparisons live for one sweep, so shared ancestors don't cause per-file stats.
type deletionFences struct {
	paths       []string
	comparisons map[[2]string]bool
}

func newDeletionFences(skipped []adapters.SkippedFile) *deletionFences {
	f := &deletionFences{comparisons: map[[2]string]bool{}}
	for _, skip := range skipped {
		if skip.Reason == "symlink_directory" || skip.Reason == "symlink_unresolved" {
			f.paths = append(f.paths, filepath.Clean(skip.Path))
		}
	}
	return f
}

func (f *deletionFences) hasSource(sources []raw.Source) bool {
	for _, path := range f.paths {
		for _, source := range sources {
			ancestor := filepath.Clean(cmp.Or(source.SourcePath, source.Path))
			rel, err := filepath.Rel(path, ancestor)
			// Incomparable namespaces cannot prove a source is outside the fence.
			if err != nil || filepath.IsLocal(rel) {
				return true
			}
			// Only a possible spelling alias at the boundary's depth needs
			// metadata. Unrelated paths keep the lexical, syscall-free fast path.
			depth := strings.Count(path, string(filepath.Separator))
			for n := strings.Count(ancestor, string(filepath.Separator)); n > depth; n-- {
				ancestor = filepath.Dir(ancestor)
			}
			if mayAliasBoundary(path, ancestor) && f.sameBoundary(path, ancestor) {
				return true
			}
		}
	}
	return false
}

// mayAliasBoundary is only a candidate filter; filesystem identity decides whether
// spellings alias. Non-ASCII components may be canonically equivalent (NFC/NFD),
// but a Unicode home path must not trigger stats for unrelated ASCII subtrees.
func mayAliasBoundary(left, right string) bool {
	if strings.EqualFold(left, right) {
		return true
	}
	if isASCII(left) && isASCII(right) {
		return false
	}
	parts := strings.Split(left, string(filepath.Separator))
	other := strings.Split(right, string(filepath.Separator))
	if len(parts) != len(other) {
		return false
	}
	for i, part := range parts {
		if !strings.EqualFold(part, other[i]) && isASCII(part) && isASCII(other[i]) {
			return false
		}
	}
	return true
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func (f *deletionFences) sameBoundary(path, ancestor string) bool {
	pair := [2]string{path, ancestor}
	if fenced, ok := f.comparisons[pair]; ok {
		return fenced
	}
	fenced := true // lookup errors or a changed boundary cannot prove absence
	defer func() { f.comparisons[pair] = fenced }()
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink == 0 {
		return fenced
	}
	st, lookupErr := os.Lstat(ancestor)
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return fenced
	}
	// With a stable skipped link, ENOENT means this spelling is distinct on a
	// case/normalization-sensitive filesystem. Other failures remain uncertain.
	if lookupErr != nil && !errors.Is(lookupErr, fs.ErrNotExist) {
		return fenced
	}
	fenced = lookupErr == nil && os.SameFile(before, st)
	return fenced
}
