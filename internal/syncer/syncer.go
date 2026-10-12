// Package syncer uploads new or changed files, then refreshes each touched session's manifest.
//
// Object layout (see docs/storage-layout.md):
//
//	raw/agent=<agent>/machine=<machine>/<key>.zst                       file bytes, zstd, unmodified
//	raw/agent=<agent>/machine=<machine>/<key>.zst.d/<start>-<end>.zst   bytes appended since, until folded
//	blobs/<sha256>.zst                                                  bytes a rewrite replaced, stored once
//	raw-versions/agent=<agent>/machine=<machine>/<key>.json             which blobs hold each earlier version
//	manifests/agent=<agent>/machine=<machine>/<session>.json            files, launch, git, snapshots
//	snapshots/blobs/<sha256>                                            harness and memory file bodies
//	snapshots/sets/<set-id>.json                                        which files made up a set
//	git/<repo-id>/...                                                   history of snapshotted repos
//
// Files are read and hashed in order on the calling goroutine; changed ones upload on a small
// pool (uploads are latency-bound, and a first backfill can be thousands of files). Local state
// is only written from the calling goroutine.
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
	"sync"
	"time"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

// Tunables, variables only so tests can shrink them.
var (
	snapshotRecheck = 600 * time.Second
	// Files bigger than this are hashed and compressed in a stream to a temp file and uploaded
	// from disk (multipart on R2), instead of being held in memory.
	largeFileBytes int64 = 64 << 20
	chunkSize      int64 = 1 << 20
)

const (
	// A session whose transcript changed this recently counts as running when a sweep
	// snapshots it.
	activeSession = 900 * time.Second
	// A transcript written to in the last minute may end mid-line; hold back the partial line
	// until the next sync instead of uploading half a JSON object.
	activeWrite   = 60 * time.Second
	uploadWorkers = 8
	prefixLines   = 20
	// A file that grew by appending and was written to within this long is still in use:
	// upload only its new bytes, as a segment. Once it has been quiet this long, the next sync
	// folds it back into one object, which also compresses better.
	foldAfter   = activeSession
	maxSegments = 64
	// Database copies are refreshed hourly (AGENT_SESSIONS_DB_INTERVAL_MINUTES); the copy a
	// refresh replaces is kept as a version at most once a day.
	dbVersionEvery = 24 * time.Hour
	maxErrorMsgs   = 10
)

var transcriptRoles = map[string]bool{"main": true, "subagent": true, "superseded": true}

// Summary counts what a run did.
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

// Trimmed is the summary as recorded in state, with at most ten error messages.
func (s *Summary) Trimmed() Summary {
	t := *s
	t.ErrorMessages = slices.Clone(s.ErrorMessages[:min(len(s.ErrorMessages), maxErrorMsgs)])
	if t.ErrorMessages == nil {
		t.ErrorMessages = []string{}
	}
	return t
}

// Found is a file to sync and the root it was found under.
type Found struct {
	File adapters.SessionFile
	Root string
}

type sessionID struct{ agent, key string }

type snapshotKey struct{ agent, root, cwd, session string }

type snapshotResult struct {
	setID    string
	memoryID *string
	mtimes   map[string]float64
}

// Options are a Syncer's settings beyond the config.
type Options struct {
	// Trigger is "manual", "sweep", "hook:<event>"...
	Trigger string
	// Snapshots turns harness snapshots and git exports on.
	Snapshots bool
	Logger    *slog.Logger
	// Version is the collector version written into manifests.
	Version string
}

// Syncer uploads files and manifests for one run.
type Syncer struct {
	cfg      *config.Config
	store    store.Store
	state    *state.State
	adapters map[string]adapters.Adapter
	opts     Options
	log      *slog.Logger
	now      func() time.Time

	Summary Summary

	roots         map[sessionID]string
	touched       map[sessionID]bool
	snapshotCache map[snapshotKey]snapshotResult
	workDir       string
	tmpDir        string
}

// New returns a Syncer for one run.
func New(cfg *config.Config, st store.Store, s *state.State, ads map[string]adapters.Adapter, opts Options) *Syncer {
	if opts.Trigger == "" {
		opts.Trigger = "manual"
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		cfg: cfg, store: st, state: s, adapters: ads, opts: opts, log: logger, now: time.Now,
		Summary:       Summary{ErrorMessages: []string{}},
		roots:         map[sessionID]string{},
		touched:       map[sessionID]bool{},
		snapshotCache: map[snapshotKey]snapshotResult{},
		workDir:       filepath.Join(cfg.StateDir, "exports"),
		tmpDir:        filepath.Join(cfg.StateDir, "tmp"),
	}
}

func (s *Syncer) nowSeconds() float64 {
	return float64(s.now().UnixNano()) / 1e9
}

func (s *Syncer) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.Summary.Errors++
	s.Summary.ErrorMessages = append(s.Summary.ErrorMessages, msg)
	s.log.Error(msg)
}

// Roots lists an agent's roots: its defaults plus any a hook reported before.
func (s *Syncer) Roots(agent string) ([]string, error) {
	extra, err := s.state.Roots(agent)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range append(s.adapters[agent].DefaultRoots(), extra...) {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// touch marks a session's manifest as needing a rewrite. The mark is kept in state until the
// manifest is uploaded, so a run that fails or is killed before then leaves it for the next.
func (s *Syncer) touch(id sessionID) error {
	s.touched[id] = true
	return s.state.MarkManifestPending(id.agent, id.key)
}

// rememberRoot notes the root a session's files were first found under.
func (s *Syncer) rememberRoot(id sessionID, root string) {
	if _, ok := s.roots[id]; !ok {
		s.roots[id] = root
	}
}

func (s *Syncer) excluded(path string) bool {
	return path == s.cfg.ConfigPath || fsutil.Within(path, s.cfg.StateDir)
}

// SweepFiles lists every file under every root, plus database copies and other exports.
// Files uploaded before and gone from disk now get a deletion date when tombstones is set.
func (s *Syncer) SweepFiles(agents []string, tombstones bool) ([]Found, error) {
	var out []Found
	seen := map[string]bool{}
	for _, agent := range agents {
		roots, err := s.Roots(agent)
		if err != nil {
			return nil, err
		}
		for _, root := range roots {
			found := s.sweepRoot(agent, root, seen, &out)
			if tombstones && fsutil.IsDir(root) {
				if err := s.tombstone(agent, root, found); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// sweepRoot appends the files under one root not already seen, and returns every path found
// there.
func (s *Syncer) sweepRoot(agent, root string, seen map[string]bool, out *[]Found) map[string]bool {
	a := s.adapters[agent]
	found := map[string]bool{}
	for _, sf := range adapters.Discover(a, root) {
		if !s.excluded(sf.Path) && !seen[sf.Path] {
			seen[sf.Path] = true
			found[sf.Path] = true
			*out = append(*out, Found{sf, root})
		}
	}
	interval := time.Duration(s.cfg.DBIntervalMinutes) * time.Minute
	for _, sf := range a.Exports(root, s.workDir, interval) {
		if !seen[sf.Path] {
			seen[sf.Path] = true
			*out = append(*out, Found{sf, root})
		}
		found[sf.Path] = true
	}
	return found
}

func (s *Syncer) tombstone(agent, root string, found map[string]bool) error {
	live, err := s.state.LiveFiles(agent, root)
	if err != nil {
		return err
	}
	now := s.nowSeconds()
	for _, f := range live {
		if found[f.Path] || fsutil.Exists(f.Path) {
			continue
		}
		if err := s.state.MarkDeleted(f.Path, now); err != nil {
			return err
		}
		s.Summary.Deleted++
		if f.SessionKey != "" {
			id := sessionID{agent, f.SessionKey}
			if err := s.touch(id); err != nil {
				return err
			}
			s.rememberRoot(id, root)
		}
	}
	return s.state.Flush()
}

// resolve classifies a path a hook reported, remembering a root found only through the hook's
// environment (CLAUDE_CONFIG_DIR...) or the path itself, so the sweep, which runs without that
// environment, knows about it.
func (s *Syncer) resolve(agent, path string) (adapters.SessionFile, string, bool, error) {
	a := s.adapters[agent]
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/') {
		path = a.Home() + rest
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	sf, root, ok, err := s.classifyReported(a, path)
	if err != nil || !ok {
		return sf, root, ok, err
	}
	if err := s.remember(agent, root); err != nil {
		return adapters.SessionFile{}, "", false, err
	}
	return sf, root, true, nil
}

// classifyReported classifies a reported path under the first known root that takes it, or
// else under the root its shape implies.
func (s *Syncer) classifyReported(a adapters.Adapter, path string) (adapters.SessionFile, string, bool, error) {
	roots, err := s.Roots(a.Name())
	if err != nil {
		return adapters.SessionFile{}, "", false, err
	}
	for _, r := range roots {
		if !fsutil.Within(path, r) {
			continue
		}
		if sf, ok := a.Classify(path, r); ok {
			return sf, r, true, nil
		}
	}
	if root := a.InferRoot(path); root != "" {
		if sf, ok := a.Classify(path, root); ok {
			return sf, root, true, nil
		}
	}
	s.log.Warn("not a recognised session file", "path", path, "agent", a.Name())
	s.Summary.Skipped++
	return adapters.SessionFile{}, "", false, nil
}

// remember stores a root other than the agent's canonical one, so later sweeps walk it too.
func (s *Syncer) remember(agent, root string) error {
	if root == s.adapters[agent].CanonicalRoot() {
		return nil
	}
	known, err := s.state.Roots(agent)
	if err != nil || slices.Contains(known, root) {
		return err
	}
	if err := s.state.AddRoot(agent, root); err != nil {
		return err
	}
	s.log.Info("remembering root", "agent", agent, "root", root)
	return s.state.Flush()
}

// HookFiles returns the files for paths a hook reported, plus their related files
// (subagents, spilled output, file history, tasks...).
func (s *Syncer) HookFiles(agent string, paths []string) ([]Found, error) {
	a := s.adapters[agent]
	var out []Found
	seen := map[string]bool{}
	for _, path := range paths {
		sf, root, ok, err := s.resolve(agent, path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, p := range append([]string{sf.Path}, a.Related(sf, root)...) {
			if seen[p] || s.excluded(p) {
				continue
			}
			if related, ok := a.Classify(p, root); ok {
				seen[p] = true
				out = append(out, Found{related, root})
			}
		}
	}
	return out, nil
}

type result struct {
	u   *upload
	err error
}

// Run syncs files, then writes the manifest of every session touched, now or in an earlier
// run that didn't get to write it.
func (s *Syncer) Run(ctx context.Context, files []Found) (*Summary, error) {
	pending, err := s.state.PendingManifests()
	if err != nil {
		return &s.Summary, err
	}
	for _, p := range pending {
		s.touched[sessionID{p.Agent, p.SessionKey}] = true
	}
	results := make(chan result, uploadWorkers*2)
	sem := make(chan struct{}, uploadWorkers)
	inflight := 0
	collect := func() error {
		r := <-results
		inflight--
		defer r.u.cleanup()
		if r.err != nil {
			s.fail(r.u.sf, r.err)
			return nil
		}
		return s.finish(r.u)
	}
	var stateErr error
	for _, f := range files {
		if ctx.Err() != nil || stateErr != nil {
			break
		}
		u, err := s.prepare(f.File, f.Root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // rotated or recompressed between discovery and read
		case err != nil: // keep going; one bad file must not block the rest
			s.fail(f.File, err)
			continue
		case u == nil:
			continue
		case !u.changed:
			stateErr = s.finish(u)
			continue
		}
		for inflight >= uploadWorkers*2 && stateErr == nil {
			stateErr = collect()
		}
		inflight++
		go func() {
			sem <- struct{}{}
			err := s.upload(ctx, u)
			<-sem
			results <- result{u, err}
		}()
	}
	for inflight > 0 {
		if err := collect(); err != nil && stateErr == nil {
			stateErr = err
		}
	}
	if stateErr != nil {
		return &s.Summary, stateErr
	}
	if err := s.writeManifests(ctx); err != nil {
		return &s.Summary, err
	}
	return &s.Summary, s.state.Flush()
}

func (s *Syncer) fail(sf adapters.SessionFile, err error) {
	s.errorf("%s: %v", sf.Path, err)
}

func (s *Syncer) rawKey(agent, key string) string {
	return "raw/agent=" + agent + "/machine=" + s.cfg.MachineID + "/" + key + ".zst"
}

func (s *Syncer) versionsKey(sf adapters.SessionFile) string {
	return "raw-versions/agent=" + sf.Agent + "/machine=" + s.cfg.MachineID + "/" + sf.Key + ".json"
}

func (s *Syncer) manifestKey(agent, sessionKey string) string {
	return "manifests/agent=" + agent + "/machine=" + s.cfg.MachineID + "/" + sessionKey + ".json"
}

// parallel runs fn over items on up to uploadWorkers goroutines and returns each item's error.
func parallel[T any](items []T, fn func(T) error) []error {
	errs := make([]error, len(items))
	sem := make(chan struct{}, uploadWorkers)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			errs[i] = fn(item)
		})
	}
	wg.Wait()
	return errs
}

func fileMtime(st os.FileInfo) float64 {
	return float64(st.ModTime().UnixNano()) / 1e9
}
