// Package syncer publishes immutable, remotely recoverable capture records.
package syncer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

var (
	snapshotRecheck       = 600 * time.Second
	largeFileSize   int64 = 64 << 20
	chunkSize       int64 = 1 << 20
)

const (
	inSessionWindow   = 900 * time.Second
	partialLineWindow = 60 * time.Second
	maxSegments       = 64
	recopyAfter       = time.Hour
	uploadWorkers     = 8
	maxHeldSize       = 128 << 20
	prefixLines       = 20
	maxErrorMsgs      = 10
)

var transcriptRoles = map[adapters.Role]bool{adapters.RoleMain: true, adapters.RoleSubagent: true, adapters.RoleSuperseded: true}

type Summary struct {
	Uploaded      int            `json:"uploaded"`
	Unchanged     int            `json:"unchanged"`
	Skipped       int            `json:"skipped"`
	SkipReasons   map[string]int `json:"skip_reasons,omitempty"`
	Errors        int            `json:"errors"`
	BytesUploaded int64          `json:"bytes_uploaded"`
	Manifests     int            `json:"manifests"`
	Versions      int            `json:"versions"`
	Segments      int            `json:"segments"`
	Folded        int            `json:"folded"`
	Deleted       int            `json:"deleted"`
	Unknown       int            `json:"unknown"`
	ErrorMessages []string       `json:"error_messages"`
}

func (s *Summary) Trimmed() Summary {
	trimmed := *s
	trimmed.ErrorMessages = slices.Clone(s.ErrorMessages[:min(len(s.ErrorMessages), maxErrorMsgs)])
	if trimmed.ErrorMessages == nil {
		trimmed.ErrorMessages = []string{}
	}
	return trimmed
}

type (
	Options struct {
		Trigger    string
		Logger     *slog.Logger
		Version    string
		Exclusions exclusion.Registry
	}
	Syncer struct {
		cfg          *config.Config
		bucket       store.Store
		db           *state.State
		adapters     map[string]adapters.Adapter
		opts         Options
		log          *slog.Logger
		clock        func() time.Time
		Summary      Summary
		policy       *capturepolicy.Policy
		policyErr    error
		warm         bool
		runToken     string
		finished     bool
		recovered    bool
		aliases      map[string][]raw.Source
		sessionRoots map[adapters.SessionRef]string
		touched      map[adapters.SessionRef]bool
		workDir      string
		tmpDir       string
		dependencies dependencyCache
	}
)

func New(cfg *config.Config, bucket store.Store, db *state.State, ads map[string]adapters.Adapter, opts Options) *Syncer {
	policy, policyErr := capturepolicy.New(cfg.Path, cfg.StateDir)
	if opts.Trigger == "" {
		opts.Trigger = "manual"
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{policy: policy, policyErr: policyErr, cfg: cfg, bucket: bucket, db: db, adapters: ads, opts: opts, log: logger, clock: time.Now, Summary: Summary{ErrorMessages: []string{}}, aliases: map[string][]raw.Source{}, sessionRoots: map[adapters.SessionRef]string{}, touched: map[adapters.SessionRef]bool{}, workDir: filepath.Join(cfg.StateDir, "exports"), tmpDir: filepath.Join(cfg.StateDir, "tmp")}
}
func (s *Syncer) nowSeconds() float64 { return state.UnixSeconds(s.clock()) }
func (s *Syncer) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.Summary.Errors++
	s.Summary.ErrorMessages = append(s.Summary.ErrorMessages, msg)
	s.log.Error("sync error", "error", msg)
}
func (s *Syncer) fail(path string, err error) { s.errorf("%s: %v", path, err) }
func (s *Syncer) roots(agent string) ([]string, error) {
	extra, err := s.db.ExtraRoots(agent)
	if err != nil {
		return nil, err
	}
	a := s.adapters[agent]
	var out []string
	for _, root := range slices.Concat(a.DefaultRoots(), []string{a.CanonicalRoot()}, extra) {
		if !slices.Contains(out, root) {
			out = append(out, root)
		}
	}
	return out, nil
}

func (s *Syncer) noteSessionRoot(ref adapters.SessionRef, root string) {
	if _, ok := s.sessionRoots[ref]; !ok {
		s.sessionRoots[ref] = root
	}
}
func (s *Syncer) excluded(path string) bool       { return s.protected(path, resolvedPath(path)) }
func (s *Syncer) rawKey(agent, key string) string { return s.publicationPrefix("file", agent, key) }
func fileMtime(info os.FileInfo) float64          { return state.UnixSeconds(info.ModTime()) }

func (s *Syncer) SweepFiles(ctx context.Context, agents []string) ([]adapters.SessionFile, error) {
	if err := s.Recover(ctx); err != nil {
		return nil, err
	}
	var out []adapters.SessionFile
	for _, agent := range agents {
		roots, err := s.roots(agent)
		if err != nil {
			return nil, err
		}
		for _, root := range roots {
			discovery := s.sweepRoot(ctx, agent, root)
			s.discoveryIssues(discovery)
			if discovery.Complete && fsutil.IsDir(root) {
				if err := s.tombstone(ctx, agent, root, discovery.Files); err != nil {
					return nil, err
				}
			}
			out = append(out, discovery.Files...)
		}
	}
	return uniqueByPath(out), nil
}

func uniqueByPath(files []adapters.SessionFile) []adapters.SessionFile {
	seen := map[string]bool{}
	return slices.DeleteFunc(files, func(sf adapters.SessionFile) bool {
		key := sf.Agent + "\x00" + sf.Key + "\x00" + sf.Path
		dup := seen[key]
		seen[key] = true
		return dup
	})
}

func (s *Syncer) skipReason(reason string) {
	s.Summary.Skipped++
	if s.Summary.SkipReasons == nil {
		s.Summary.SkipReasons = map[string]int{}
	}
	s.Summary.SkipReasons[reason]++
}

func (s *Syncer) discoveryIssues(discovery adapters.Discovery) {
	for _, skipped := range discovery.Skipped {
		s.skipReason(skipped.Reason)
	}
	for _, issue := range discovery.Issues {
		s.errorf("discovery %s (%s): %v", issue.Path, issue.Kind, issue.Err)
	}
}

func (s *Syncer) sweepRoot(ctx context.Context, agent, root string) adapters.Discovery {
	a := s.adapters[agent]
	found := adapters.Discover(a, root)
	found.Files = slices.DeleteFunc(found.Files, s.excludedFile)
	found.Databases = slices.DeleteFunc(found.Databases, func(path string) bool {
		return s.opts.Exclusions.Excludes(adapters.SessionFile{Agent: agent, Root: root, Path: path}) || s.excluded(path)
	})
	exports := adapters.SQLiteExports(ctx, a, root, found.Databases, s.workDir, recopyAfter)
	if !s.opts.Exclusions.Excludes(adapters.SessionFile{Agent: agent, Root: root}) {
		extra := a.Exports(root, s.workDir, recopyAfter)
		exports.Files = append(exports.Files, extra.Files...)
		exports.Issues = append(exports.Issues, extra.Issues...)
		exports.Skipped = append(exports.Skipped, extra.Skipped...)
		exports.Complete = exports.Complete && extra.Complete
	}
	found.Files = append(found.Files, exports.Files...)
	found.Issues = append(found.Issues, exports.Issues...)
	found.Skipped = append(found.Skipped, exports.Skipped...)
	found.Complete = found.Complete && exports.Complete
	return found
}

func (s *Syncer) tombstone(ctx context.Context, agent, root string, onDisk []adapters.SessionFile) error {
	present := map[string]bool{}
	for _, sf := range onDisk {
		present[sf.Key] = true
	}
	streams, err := s.db.ArchiveStreams("file")
	if err != nil {
		return err
	}
	for _, stream := range streams {
		record, err := s.db.CurrentArchive("file", stream)
		if err != nil {
			return err
		}
		p, err := decodePublication(record.Body)
		if err != nil {
			return err
		}
		g := p.File
		if p.Agent != agent || g.Identity.Root != root || g.DeletedAt > 0 || present[p.Key] || s.excludedPublication(p) {
			continue
		}
		absent, err := s.sourcesAbsent(g.Sources)
		if err != nil {
			return err
		}
		if !absent {
			continue
		}
		if err := s.publishDeletion(ctx, p, record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) sourcesAbsent(sources []raw.Source) (bool, error) {
	absent := true
	for _, source := range sources {
		path := cmp.Or(source.SourcePath, source.Path)
		// Policy exclusions withhold future captures; they must not invent a deletion
		// of history, including exports whose disposable staging has disappeared.
		if s.excluded(path) {
			return false, nil
		}
		if _, err := os.Stat(path); err == nil {
			absent = false
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("tombstone %s: %w", path, err)
		}
	}
	return absent, nil
}

func (s *Syncer) publishDeletion(ctx context.Context, p *publication, record *state.Record) error {
	g := p.File

	previous := *g
	next := *g
	next.Pieces = slices.Clone(g.Pieces)
	next.Sequence++
	next.Parent = record.ID
	next.Kind = "delete"
	next.DeletedAt = s.nowSeconds()
	next.ObservedAt = next.DeletedAt
	deletion := &publication{Schema: 3, MachineID: s.cfg.MachineID, Kind: "file", Agent: p.Agent, Key: p.Key, Sequence: next.Sequence, Parent: next.Parent, File: &next}
	r, err := s.publish(ctx, deletion, nil)
	if err != nil {
		return err
	}
	if err := s.db.CommitFile(r, s.rawKey(p.Agent, p.Key), &next, &previous); err != nil {
		return err
	}
	s.Summary.Deleted++
	if next.Identity.SessionKey != "" {
		ref := adapters.SessionRef{Agent: p.Agent, Key: next.Identity.SessionKey}
		s.touched[ref] = true
		s.noteSessionRoot(ref, g.Identity.Root)
	}
	return nil
}

func (s *Syncer) classifyHookPath(ctx context.Context, agent, path string) (adapters.SessionFile, bool, error) {
	a := s.adapters[agent]
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/') {
		path = a.Home() + rest
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if s.excluded(path) || s.opts.Exclusions.ExcludesPath(a, path) {
		return adapters.SessionFile{}, false, nil
	}
	sf, ok, err := s.classifyReported(a, path)
	if err != nil || !ok {
		return sf, ok, err
	}
	if err := s.rememberExtraRoot(ctx, agent, sf.Root); err != nil {
		return adapters.SessionFile{}, false, err
	}
	return sf, true, nil
}

func (s *Syncer) classifyReported(a adapters.Adapter, path string) (adapters.SessionFile, bool, error) {
	roots, err := s.roots(a.Name())
	if err != nil {
		return adapters.SessionFile{}, false, err
	}
	for _, root := range roots {
		if fsutil.Within(path, root) {
			if sf, ok := a.Classify(path, root); ok {
				return sf, true, nil
			}
		}
	}
	if root := a.InferRoot(path); root != "" {
		if sf, ok := a.Classify(path, root); ok {
			return sf, true, nil
		}
	}
	s.Summary.Skipped++
	return adapters.SessionFile{}, false, nil
}

func (s *Syncer) rememberExtraRoot(ctx context.Context, agent, root string) error {
	if !raw.ValidPath(root) {
		return errors.New("archive root: unsupported path identity; requires absolute UTF-8 without NUL")
	}
	if root == s.adapters[agent].CanonicalRoot() {
		return nil
	}
	key := streamID(agent, root)
	existing, _, err := s.current("root", agent, key)
	if err != nil || existing != nil {
		return err
	}
	p := &publication{Schema: 3, MachineID: s.cfg.MachineID, Kind: "root", Agent: agent, Key: key, Sequence: 1, Root: &rootRecord{Agent: agent, Path: root}}
	r, err := s.publish(ctx, p, nil)
	if err != nil {
		return err
	}
	return s.applyPublication(r, p, nil)
}

func (s *Syncer) HookFilesContext(ctx context.Context, agent string, paths []string) ([]adapters.SessionFile, error) {
	if !s.recovered {
		return nil, errors.New("syncer: Recover must precede HookFiles")
	}
	a := s.adapters[agent]
	var out []adapters.SessionFile
	for _, path := range paths {
		sf, ok, err := s.classifyHookPath(ctx, agent, path)
		if err != nil {
			return nil, err
		}
		if !ok || s.excludedFile(sf) {
			continue
		}
		related := a.Related(sf)
		s.discoveryIssues(related)
		related.Files = append(related.Files, sf)
		for _, candidate := range related.Files {
			if !s.excludedFile(candidate) {
				out = append(out, candidate)
			}
		}
	}
	return uniqueByPath(out), nil
}

type (
	result struct {
		u   *upload
		err error
	}
	pool struct {
		s        *Syncer
		ctx      context.Context
		results  chan result
		sem      chan struct{}
		inflight int
		held     int64
	}
)

func (p *pool) add(u *upload) error {
	for p.inflight >= uploadWorkers*2 || p.inflight > 0 && p.held+u.held() > maxHeldSize {
		if err := p.collect(); err != nil {
			return err
		}
	}
	p.inflight++
	p.held += u.held()
	go func() { p.sem <- struct{}{}; err := p.s.upload(p.ctx, u); <-p.sem; p.results <- result{u, err} }()
	return nil
}

func (p *pool) collect() error {
	r := <-p.results
	p.inflight--
	p.held -= r.u.held()
	defer r.u.cleanup()
	if errors.Is(r.err, errVanished) {
		return nil
	}
	if r.err != nil {
		p.s.fail(r.u.file.Path, r.err)
		return nil
	}
	if err := p.s.finish(r.u); err != nil {
		return err
	}
	return p.s.refreshStream(p.ctx, "file", r.u.file.Agent, r.u.file.Key)
}

func (p *pool) wait() error {
	var first error
	for p.inflight > 0 {
		if err := p.collect(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Run is a one-shot capture lifecycle. Construct a new Syncer for a subsequent run.
func (s *Syncer) Run(ctx context.Context, files []adapters.SessionFile) (*Summary, error) {
	if s.finished {
		return &s.Summary, errors.New("syncer: Run lifecycle already finished; create a new Syncer")
	}
	s.finished = true
	if err := s.Recover(ctx); err != nil {
		return &s.Summary, err
	}
	s.clearStaging()
	files, err := s.groupAliases(ctx, files)
	if err != nil {
		return &s.Summary, err
	}
	pending, err := s.db.PendingManifests()
	if err != nil {
		return &s.Summary, err
	}
	for _, ref := range pending {
		s.touched[ref] = true
	}
	p := &pool{s: s, ctx: ctx, results: make(chan result, uploadWorkers*2), sem: make(chan struct{}, uploadWorkers)}
	var stateErr error
	for _, sf := range files {
		if ctx.Err() != nil || stateErr != nil {
			break
		}
		u, err := s.prepare(ctx, sf)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			s.fail(sf.Path, err)
		case u == nil:
		case u.outcome.Status == captureUnchanged:
			stateErr = s.finish(u)
		default:
			stateErr = p.add(u)
		}
	}
	if err := p.wait(); err != nil && stateErr == nil {
		stateErr = err
	}
	if stateErr != nil {
		return &s.Summary, stateErr
	}
	if err := ctx.Err(); err != nil {
		return &s.Summary, err
	}
	if err := s.writeManifests(ctx); err != nil {
		return &s.Summary, err
	}
	if s.Summary.Errors == 0 {
		if err := s.sealCheckpoint(ctx); err != nil {
			return &s.Summary, err
		}
	}
	return &s.Summary, nil
}

func (s *Syncer) clearStaging() {
	entries, _ := os.ReadDir(s.tmpDir)
	for _, entry := range entries {
		_ = os.Remove(filepath.Join(s.tmpDir, entry.Name()))
	}
} // disposable staging, recaptured on retry
