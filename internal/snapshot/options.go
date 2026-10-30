package snapshot

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/safefile"
	"github.com/mich-murphy/memory/internal/scrub"
)

// AssetEvidence is local-only policy evidence. Selector names the trusted selection
// route, not a caller-provided permission to traverse arbitrary Home contents.
type AssetEvidence struct {
	OriginalPath string
	Identities   []string
	Scope        string
	Selector     string
	InputKind    string
}

// Options supplies effective candidate policy. Nil Policy requires explicit policy
// locations; an explicit Policy plus a nil Exclusions means an empty registry.
// Authorize runs before any original asset bytes are read; it may add evidence,
// but cannot replace the collector's original identities or selection coordinates.
type Options struct {
	WorkingDirectory string
	Policy           *capturepolicy.Policy
	Exclusions       exclusion.Registry
	ConfigPath       string
	PolicyDir        string
	UserBase         string
	Home             string
	Authorize        func(context.Context, AssetEvidence) (AssetEvidence, error)
}

type buildState struct {
	ctx     context.Context
	options *Options
}

func initCollected(agent string, states []*buildState) *collected {
	if len(states) == 0 {
		return newCollected(agent)
	}
	state := states[0]
	return &collected{budget: &budget{}, walks: map[string]*walk{}, agent: agent, skipped: []SkippedFile{}, counts: map[string]int{}, seen: map[scopedName]bool{}, policy: state.options.Policy, ctx: state.ctx, options: state.options}
}

const maxEvidenceBytes = 2 << 20

func cloneEvidence(e AssetEvidence) AssetEvidence {
	e.Identities = slices.Clone(e.Identities)
	return e
}

func sameEvidence(a, b AssetEvidence) bool {
	return a.OriginalPath == b.OriginalPath && a.Scope == b.Scope && a.Selector == b.Selector && a.InputKind == b.InputKind && slices.Equal(a.Identities, b.Identities)
}

func (c *collected) authorize(path string, identities []string, scope, selector, kind string) (AssetEvidence, error) {
	e := AssetEvidence{OriginalPath: path, Identities: append([]string(nil), identities...), Scope: scope, Selector: selector, InputKind: kind}
	if err := c.ctx.Err(); err != nil {
		return e, err
	}
	if c.options.Authorize == nil {
		return e, errors.New("snapshot: authorization callback required")
	}
	approved, err := c.options.Authorize(c.ctx, cloneEvidence(e))
	if err != nil {
		return e, err
	}
	if !sameEvidence(e, approved) {
		return e, errors.New("snapshot: authorization changed trusted coordinates")
	}
	encoded, err := jsonx.SortedCompact(e)
	if err != nil {
		return e, err
	}
	if len(encoded) > maxEvidenceBytes-c.budget.evidence {
		return e, errors.New("snapshot: evidence capacity exceeded")
	}
	c.budget.evidence += len(encoded)
	// Retain the validated callback result, with independently owned identity storage.
	if len(c.evidence) >= maxWalkFiles*2 {
		return e, errors.New("snapshot: evidence capacity exceeded")
	}
	c.evidence = append(c.evidence, cloneEvidence(approved))
	return e, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (c *collected) readBounded(path string, limit int64, expected fs.FileInfo) ([]byte, error) {
	if c.ctx == nil {
		return readBounded(path, limit)
	}
	f, err := safefile.OpenResolved(c.ctx, path, expected)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("snapshot: nonregular source")
	}
	return io.ReadAll(io.LimitReader(contextReader{c.ctx, f}, limit+1))
}

type effectiveAdapter struct {
	adapters.Adapter
	state          *buildState
	userBase, home string
	generated      *collected
}

func (a *effectiveAdapter) UserBase(string) string { return a.userBase }
func (a *effectiveAdapter) Home() string           { return a.home }
func (a *effectiveAdapter) read(path, scope, selector, kind string, claude bool) ([]byte, []string, error) {
	r, err := capturepolicy.Resolve(path)
	if err != nil {
		return nil, r.Identities, err
	}
	excluded := a.state.options.Policy.Excludes(r.Identities)
	if claude {
		excluded = a.state.options.Policy.ExcludesClaudeConfig(r.Identities)
	}
	if excluded {
		return nil, r.Identities, errors.New("snapshot: policy excludes input")
	}
	st, err := os.Stat(r.Path)
	if err != nil {
		return nil, r.Identities, err
	}
	if err := a.generated.allow(path, r.Identities, scope, selector, kind); err != nil {
		return nil, r.Identities, err
	}
	body, err := a.generated.readBounded(r.Path, maxFileSize, st)
	if err != nil {
		return nil, r.Identities, err
	}
	if len(body) > maxFileSize {
		return nil, r.Identities, errors.New("snapshot: input too large")
	}
	body, err = scrub.Identities(a.Name(), r.Identities, body)
	if err != nil {
		return nil, r.Identities, err
	}
	if err := scrub.Text(r.Identities, body); err != nil {
		return nil, r.Identities, err
	}
	return body, r.Identities, nil
}

func (a *effectiveAdapter) Plugins(root string) adapters.Sources {
	if c, ok := a.Adapter.(*adapters.Claude); ok {
		return c.SnapshotPlugins(root, func(p string) ([]byte, error) {
			b, _, e := a.read(p, "plugin", "installed-plugins", "plugin-manifest", false)
			return b, e
		})
	}
	return adapters.Sources{Complete: true}
}

func (a *effectiveAdapter) ExtraSnapshotEntries(root, project string) adapters.Sources {
	switch original := a.Adapter.(type) {
	case *adapters.Claude:
		source := filepath.Join(root, ".claude.json")
		if root == filepath.Join(a.home, ".claude") {
			source = filepath.Join(a.home, ".claude.json")
		}
		return original.SnapshotMCP(root, project, source, func(p string) ([]byte, []string, error) {
			return a.read(p, "user", "claude-mcp", "generated-subset", true)
		})
	case *adapters.Pi:
		return original.SnapshotPackages(a.state.ctx, a.userBase, a.state.options.Policy, a.state.options.WorkingDirectory, func(p string) error {
			r, err := capturepolicy.Resolve(p)
			if err != nil {
				return err
			}
			if a.state.options.Policy.Excludes(r.Identities) {
				return errors.New("snapshot: excluded package input")
			}
			err = a.generated.allow(p, r.Identities, "user", "pi-git:"+a.userBase, "package-provenance")
			return err
		})
	default:
		return adapters.Sources{Complete: true}
	}
}

func (c *collected) allow(path string, ids []string, scope, selector, kind string) error {
	_, err := c.authorize(path, ids, scope, selector, kind)
	return err
}

func effectiveOptions(a adapters.Adapter, root string, options Options) (Options, error) {
	if options.Authorize == nil {
		return options, errors.New("snapshot: authorization callback required")
	}
	if options.Policy == nil {
		if options.ConfigPath == "" || options.PolicyDir == "" {
			return options, errors.New("snapshot: explicit effective policy required")
		}
		var err error
		options.Policy, err = capturepolicy.New(options.ConfigPath, options.PolicyDir)
		if err != nil {
			return options, err
		}
		options.Exclusions, err = exclusion.Load(options.PolicyDir)
		if err != nil {
			return options, err
		}
	}
	if options.Home == "" {
		options.Home = a.Home()
	}
	if options.UserBase == "" {
		if a.Name() == "pi" {
			return options, errors.New("snapshot: explicit Pi user base required")
		}
		options.UserBase = root
	}
	return options, nil
}

// BuildWithOptions never consults installed collector config or environment roots.
func BuildWithOptions(ctx context.Context, a adapters.Adapter, root, cwd, sessionKey string, options Options) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var err error
	options, err = effectiveOptions(a, root, options)
	if err != nil {
		return nil, err
	}
	state := &buildState{ctx: ctx, options: &options}
	ea := &effectiveAdapter{Adapter: a, state: state, userBase: options.UserBase, home: options.Home, generated: initCollected(a.Name(), []*buildState{state})}
	withheld := options.Exclusions.Excludes(adapters.SessionFile{Agent: a.Name(), Root: root})
	shared := &budget{}
	ea.generated.budget = shared
	project, isRepo := "", false
	if cwd != "" && fsutil.IsDir(cwd) {
		project, isRepo = findProjectRoot(cwd)
	}
	harness := collect(ctx, ea, root, project, isRepo, shared, withheld, state)
	memory := initCollected(a.Name(), []*buildState{state})
	memory.budget = shared
	if withheld {
		memory.skip("user", root, "session_exclusion", 0)
	} else {
		memory = collectMemory(ea, root, project, sessionKey, shared, state)
	}
	s := &Snapshot{Blobs: map[string][]byte{}, Mtimes: map[string]float64{}}
	s.Harness, err = s.set("harness", ea, root, project, harness)
	if err != nil {
		return nil, err
	}
	if len(memory.entries) > 0 || len(memory.skipped) > 0 {
		s.Memory, err = s.set("memory", ea, root, project, memory)
		if err != nil {
			return nil, err
		}
	}
	s.Complete = s.Harness.Complete && (s.Memory == nil || s.Memory.Complete)
	s.Evidence = append(s.Evidence, harness.evidence...)
	s.Evidence = append(s.Evidence, memory.evidence...)
	s.Evidence = append(s.Evidence, ea.generated.evidence...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s, nil
}
