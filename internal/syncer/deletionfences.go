package syncer

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
			// Only a case-fold-equivalent ancestor at the boundary's depth needs
			// metadata. Unrelated paths keep the lexical, syscall-free fast path.
			depth := strings.Count(path, string(filepath.Separator))
			for n := strings.Count(ancestor, string(filepath.Separator)); n > depth; n-- {
				ancestor = filepath.Dir(ancestor)
			}
			if strings.EqualFold(path, ancestor) && f.sameBoundary(path, ancestor) {
				return true
			}
		}
	}
	return false
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
	// case-sensitive filesystem. Other lookup failures remain uncertain.
	if lookupErr != nil && !errors.Is(lookupErr, fs.ErrNotExist) {
		return fenced
	}
	fenced = lookupErr == nil && os.SameFile(before, st)
	return fenced
}
