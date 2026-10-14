package capturepolicy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxSymlinks = 255

// Resolution retains every full path identity encountered while resolving ancestor and leaf
// symlinks. Path is usable only on success. Identities also includes partial resolution on error.
// Like filepath.EvalSymlinks, this does not prevent hostile concurrent filesystem replacement.
type Resolution struct {
	Path       string
	Identities []string
}

// Resolve walks path components without cleaning away .. before following symlinks. Each
// symlink replacement records its full path with the remaining suffix, not just the final leaf.
// Missing, unreadable, cyclic and excessively long chains return an error: callers must not read.
func Resolve(path string) (Resolution, error) {
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return Resolution{}, fmt.Errorf("capturepolicy: working directory: %w", err)
		}
		path = cwd + string(filepath.Separator) + path
	}
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	dest := root
	pending := components(path[len(volume):])
	result := Resolution{Identities: []string{path}}
	seen := map[string]bool{}
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case ".":
			continue
		case "..":
			dest = filepath.Dir(dest)
			continue
		}
		candidate := filepath.Join(dest, part)
		result.record(remainingPath(candidate, pending))
		st, err := os.Lstat(candidate)
		if err != nil {
			return result, fmt.Errorf("capturepolicy: resolve: %w", err)
		}
		linked := st.Mode()&os.ModeSymlink != 0
		if !linked && len(pending) > 0 && !st.IsDir() {
			return result, fmt.Errorf("capturepolicy: resolve: %w", syscall.ENOTDIR)
		}
		if !linked {
			dest = candidate
			continue
		}
		identity := remainingPath(candidate, pending)
		if seen[identity] || links >= maxSymlinks {
			return result, errors.New("capturepolicy: cyclic or excessive symlink chain")
		}
		seen[identity] = true
		links++
		target, err := os.Readlink(candidate)
		if err != nil {
			return result, fmt.Errorf("capturepolicy: read symlink: %w", err)
		}
		if filepath.IsAbs(target) {
			volume = filepath.VolumeName(target)
			dest = volume + string(filepath.Separator)
			target = target[len(volume):]
		}
		pending = append(components(target), pending...)
		result.record(remainingPath(dest, pending))
	}
	result.Path = filepath.Clean(dest)
	result.record(result.Path)
	return result, nil
}

func (r *Resolution) record(identity string) {
	if r.Identities[len(r.Identities)-1] != identity {
		r.Identities = append(r.Identities, identity)
	}
}

func components(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) })
}

// Do not use filepath.Join here: it would erase .. before its preceding symlink is resolved.
func remainingPath(prefix string, suffix []string) string {
	if len(suffix) == 0 {
		return prefix
	}
	return strings.TrimRight(prefix, string(filepath.Separator)) + string(filepath.Separator) + strings.Join(suffix, string(filepath.Separator))
}

// Policy caches every identity of the collector's active config and state locations.
// It covers known credential/config boundaries, not arbitrary secret-bearing documents.
type Policy struct {
	configs []string
	states  []string
}

// New resolves collector locations once. Missing locations still retain the logical and
// resolved-parent identities (so backups are excluded); other resolution errors fail closed.
func New(configPath, stateDir string) (*Policy, error) {
	p := &Policy{}
	for _, location := range []struct {
		path       string
		identities *[]string
	}{{configPath, &p.configs}, {stateDir, &p.states}} {
		if location.path == "" {
			continue
		}
		resolution, err := Resolve(location.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		*location.identities = resolution.Identities
	}
	return p, nil
}

// Excludes checks all supplied intermediate identities before any content is read.
func (p *Policy) Excludes(identities []string) bool {
	for _, identity := range identities {
		if IsCredential(identity) {
			return true
		}
		for _, configPath := range p.configs {
			if isConfig(identity, configPath) {
				return true
			}
		}
		for _, stateDir := range p.states {
			if isWithin(identity, stateDir) {
				return true
			}
		}
	}
	return false
}
