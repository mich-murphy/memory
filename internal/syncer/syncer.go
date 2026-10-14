// Package syncer uploads new or changed files, then refreshes each touched session's manifest,
// in the layout docs/storage-layout.md describes.
//
// Files are read and hashed in order on the calling goroutine and uploaded on a small pool.
// Local state is only written from the calling goroutine.
package syncer

import (
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

	"golang.org/x/sync/errgroup"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

// Variables so tests can shrink them.
var (
	snapshotRecheck       = 600 * time.Second
	largeFileSize   int64 = 64 << 20
	chunkSize       int64 = 1 << 20
)

const (
	inSessionWindow   = 900 * time.Second
	partialLineWindow = 60 * time.Second
	foldAfter         = inSessionWindow
	maxSegments       = 64
	dbVersionEvery    = 24 * time.Hour
	recopyAfter       = time.Hour
	uploadWorkers     = 8
	maxHeldSize       = 128 << 20
	prefixLines       = 20
	maxErrorMsgs      = 10
)

var transcriptRoles = map[adapters.Role]bool{
	adapters.RoleMain: true, adapters.RoleSubagent: true, adapters.RoleSuperseded: true,
}

type Summary struct {
	Uploaded      int      `json:"uploaded"`
	Unchanged     int      `json:"unchanged"`
	Skipped       int      `json:"skipped"`
	Errors        int      `json:"errors"`
	BytesUploaded int64    `json:"bytes_uploaded"`
	Manifests     int      `json:"manifests"`
	Versions      int      `json:"versions"`
	Segments      int      `json:"segments"`
	Folded        int      `json:"folded"`
	Deleted       int      `json:"deleted"`
	Unknown       int      `json:"unknown"`
	ErrorMessages []string `json:"error_messages"`
}

func (s *Summary) Trimmed() Summary {
	t := *s
	t.ErrorMessages = slices.Clone(s.ErrorMessages[:min(len(s.ErrorMessages), maxErrorMsgs)])
	if t.ErrorMessages == nil {
		t.ErrorMessages = []string{}
	}
	return t
}

type snapshotKey struct{ session, root, cwd string }

type Options struct {
	Trigger string
	Logger  *slog.Logger
	Version string
}

type Syncer struct {
	cfg      *config.Config
	bucket   store.Store
	db       *state.State
	adapters map[string]adapters.Adapter
	opts     Options
	log      *slog.Logger
	clock    func() time.Time

	Summary Summary

	sessionRoots  map[adapters.SessionRef]string
	touched       map[adapters.SessionRef]bool
	snapshotCache map[snapshotKey]state.SnapshotEntry
	// strays holds, by key, the objects an earlier run left that this one couldn't delete.
	strays  map[string][]string
	workDir string
	tmpDir  string
}

func New(cfg *config.Config, bucket store.Store, db *state.State, ads map[string]adapters.Adapter, opts Options) *Syncer {
	if opts.Trigger == "" {
		opts.Trigger = "manual"
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		cfg: cfg, bucket: bucket, db: db, adapters: ads, opts: opts, log: logger, clock: time.Now,
		Summary:       Summary{ErrorMessages: []string{}},
		sessionRoots:  map[adapters.SessionRef]string{},
		touched:       map[adapters.SessionRef]bool{},
		snapshotCache: map[snapshotKey]state.SnapshotEntry{},
		workDir:       filepath.Join(cfg.StateDir, "exports"),
		tmpDir:        filepath.Join(cfg.StateDir, "tmp"),
	}
}

func (s *Syncer) nowSeconds() float64 {
	return state.UnixSeconds(s.clock())
}

func (s *Syncer) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.Summary.Errors++
	s.Summary.ErrorMessages = append(s.Summary.ErrorMessages, msg)
	s.log.Error("sync error", "error", msg)
}

// roots always includes the canonical root: a sweep runs with the environment of whichever
// agent's hook started it, and CLAUDE_CONFIG_DIR and friends there would otherwise hide it.
func (s *Syncer) roots(agent string) ([]string, error) {
	extra, err := s.db.ExtraRoots(agent)
	if err != nil {
		return nil, err
	}
	a := s.adapters[agent]
	var out []string
	for _, r := range slices.Concat(a.DefaultRoots(), []string{a.CanonicalRoot()}, extra) {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Syncer) markManifestPending(ref adapters.SessionRef) error {
	s.touched[ref] = true
	return s.db.MarkManifestPending(ref)
}

func (s *Syncer) noteSessionRoot(ref adapters.SessionRef, root string) {
	if _, ok := s.sessionRoots[ref]; !ok {
		s.sessionRoots[ref] = root
	}
}

func (s *Syncer) excluded(path string) bool {
	return path == s.cfg.Path || fsutil.Within(path, s.cfg.StateDir)
}

func (s *Syncer) SweepFiles(ctx context.Context, agents []string) ([]adapters.SessionFile, error) {
	var out []adapters.SessionFile
	for _, agent := range agents {
		roots, err := s.roots(agent)
		if err != nil {
			return nil, err
		}
		for _, root := range roots {
			files := s.sweepRoot(ctx, agent, root)
			if fsutil.IsDir(root) {
				if err := s.tombstone(agent, root, files); err != nil {
					return nil, err
				}
			}
			out = append(out, files...)
		}
	}
	return uniqueByPath(out), nil
}

func uniqueByPath(files []adapters.SessionFile) []adapters.SessionFile {
	seen := map[string]bool{}
	return slices.DeleteFunc(files, func(sf adapters.SessionFile) bool {
		dup := seen[sf.Path]
		seen[sf.Path] = true
		return dup
	})
}

// sweepRoot leaves the exports in, although they live under the state dir.
func (s *Syncer) sweepRoot(ctx context.Context, agent, root string) []adapters.SessionFile {
	a := s.adapters[agent]
	files, databases := adapters.Discover(a, root)
	files = slices.DeleteFunc(files, func(sf adapters.SessionFile) bool { return s.excluded(sf.Path) })
	return slices.Concat(files, adapters.SQLiteExports(ctx, a, root, databases, s.workDir, recopyAfter),
		a.Exports(root, s.workDir, recopyAfter))
}

func (s *Syncer) tombstone(agent, root string, onDisk []adapters.SessionFile) error {
	present := map[string]bool{}
	for _, sf := range onDisk {
		present[sf.Path] = true
	}
	live, err := s.db.LiveFiles(agent, root)
	if err != nil {
		return err
	}
	now := s.nowSeconds()
	for _, f := range live {
		if present[f.Path] || fsutil.Exists(f.Path) {
			continue
		}
		if err := s.db.MarkDeleted(f.Path, now); err != nil {
			return err
		}
		s.Summary.Deleted++
		if f.SessionKey != "" {
			ref := f.Session()
			if err := s.markManifestPending(ref); err != nil {
				return err
			}
			s.noteSessionRoot(ref, root)
		}
	}
	return s.db.Flush()
}

func (s *Syncer) classifyHookPath(agent, path string) (adapters.SessionFile, bool, error) {
	a := s.adapters[agent]
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/') {
		path = a.Home() + rest
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	sf, ok, err := s.classifyReported(a, path)
	if err != nil || !ok {
		return sf, ok, err
	}
	if err := s.rememberExtraRoot(agent, sf.Root); err != nil {
		return adapters.SessionFile{}, false, err
	}
	return sf, true, nil
}

func (s *Syncer) classifyReported(a adapters.Adapter, path string) (adapters.SessionFile, bool, error) {
	roots, err := s.roots(a.Name())
	if err != nil {
		return adapters.SessionFile{}, false, err
	}
	for _, r := range roots {
		if !fsutil.Within(path, r) {
			continue
		}
		if sf, ok := a.Classify(path, r); ok {
			return sf, true, nil
		}
	}
	if root := a.InferRoot(path); root != "" {
		if sf, ok := a.Classify(path, root); ok {
			return sf, true, nil
		}
	}
	s.log.Warn("not a recognised session file", "path", path, "agent", a.Name())
	s.Summary.Skipped++
	return adapters.SessionFile{}, false, nil
}

func (s *Syncer) rememberExtraRoot(agent, root string) error {
	if root == s.adapters[agent].CanonicalRoot() {
		return nil
	}
	known, err := s.db.ExtraRoots(agent)
	if err != nil || slices.Contains(known, root) {
		return err
	}
	if err := s.db.AddExtraRoot(agent, root); err != nil {
		return err
	}
	s.log.Info("remembering root", "agent", agent, "root", root)
	return s.db.Flush()
}

func (s *Syncer) HookFiles(agent string, paths []string) ([]adapters.SessionFile, error) {
	a := s.adapters[agent]
	var out []adapters.SessionFile
	for _, path := range paths {
		sf, ok, err := s.classifyHookPath(agent, path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, p := range append([]string{sf.Path}, a.Related(sf)...) {
			if related, ok := a.Classify(p, sf.Root); ok && !s.excluded(p) {
				out = append(out, related)
			}
		}
	}
	return uniqueByPath(out), nil
}

type result struct {
	u   *upload
	err error
}

// pool runs uploads on uploadWorkers goroutines, with at most twice that many, holding at
// most maxHeldSize of files, in flight. Results are handled on the caller's goroutine, which
// alone writes local state.
type pool struct {
	s        *Syncer
	ctx      context.Context
	results  chan result
	sem      chan struct{}
	inflight int
	held     int64
}

func (p *pool) add(u *upload) error {
	for p.inflight >= uploadWorkers*2 || p.inflight > 0 && p.held+u.held() > maxHeldSize {
		if err := p.collect(); err != nil {
			return err
		}
	}
	p.inflight++
	p.held += u.held()
	go func() {
		p.sem <- struct{}{}
		err := p.s.upload(p.ctx, u)
		<-p.sem
		p.results <- result{u, err}
	}()
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
	return p.s.finish(r.u)
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

func (s *Syncer) Run(ctx context.Context, files []adapters.SessionFile) (*Summary, error) {
	s.clearStaging()
	if err := s.deleteStrays(ctx); err != nil {
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
		u, err := s.prepare(sf)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // rotated or recompressed between discovery and read
		case err != nil:
			s.fail(sf.Path, err)
		case u == nil:
		case !u.needsUpload:
			stateErr = s.finish(u)
		default:
			stateErr = s.send(p, u)
		}
	}
	if err := p.wait(); err != nil && stateErr == nil {
		stateErr = err
	}
	if stateErr != nil {
		return &s.Summary, stateErr
	}
	if err := s.writeManifests(ctx); err != nil {
		return &s.Summary, err
	}
	return &s.Summary, s.db.Flush()
}

// send records an upload over a key's earlier pieces as a stray until its answer is recorded,
// so a run that never hears back is cleaned up after. A first upload needs no record: with
// nothing recorded, the next run uploads the file whole again.
func (s *Syncer) send(p *pool, u *upload) error {
	if u.piece.Start > 0 || len(u.replaced) > 0 {
		if err := s.db.AddStray(state.Stray{Key: u.key, Object: u.piece.Object}); err != nil {
			return err
		}
		if err := s.db.Flush(); err != nil {
			return err
		}
	}
	return p.add(u)
}

// clearStaging removes temp copies of large files that a killed run left behind.
func (s *Syncer) clearStaging() {
	entries, _ := os.ReadDir(s.tmpDir) // staging reports a tmp dir it can't use
	for _, e := range entries {
		_ = os.Remove(filepath.Join(s.tmpDir, e.Name())) // one left now is removed by the next run
	}
}

// deleteStrays deletes the segments earlier runs left, since a reader would take one for the
// bytes after its start. A key whose base or a segment is still stray is uploaded whole.
func (s *Syncer) deleteStrays(ctx context.Context) error {
	strays, err := s.db.Strays()
	if err != nil || len(strays) == 0 {
		return err
	}
	errs := parallel(strays, func(x state.Stray) error {
		if x.Object == x.Key {
			return nil
		}
		return s.bucket.Delete(ctx, x.Object)
	})
	s.strays = map[string][]string{}
	for i, x := range strays {
		if x.Object == x.Key || errs[i] != nil {
			s.strays[x.Key] = append(s.strays[x.Key], x.Object)
			continue
		}
		if err := s.db.RemoveStray(x); err != nil {
			return err
		}
	}
	return s.db.Flush()
}

func (s *Syncer) fail(path string, err error) {
	s.errorf("%s: %v", path, err)
}

func (s *Syncer) rawKey(agent, key string) string {
	return "raw/agent=" + agent + "/machine=" + s.cfg.MachineID + "/" + key + ".zst"
}

func (s *Syncer) versionsKey(sf adapters.SessionFile) string {
	return "raw-versions/agent=" + sf.Agent + "/machine=" + s.cfg.MachineID + "/" + sf.Key + ".json"
}

func (s *Syncer) manifestKey(ref adapters.SessionRef) string {
	return "manifests/agent=" + ref.Agent + "/machine=" + s.cfg.MachineID + "/" + ref.Key + ".json"
}

func parallel[T any](items []T, fn func(T) error) []error {
	errs := make([]error, len(items))
	var g errgroup.Group
	g.SetLimit(uploadWorkers)
	for i, item := range items {
		g.Go(func() error {
			errs[i] = fn(item)
			return nil
		})
	}
	_ = g.Wait() // each item's error is kept in errs, so the group's is always nil
	return errs
}

func fileMtime(st os.FileInfo) float64 {
	return state.UnixSeconds(st.ModTime())
}
