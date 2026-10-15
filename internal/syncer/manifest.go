package syncer

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/gitinfo"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/snapshot"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

func (s *Syncer) RecordEnvelope(ctx context.Context, envelope launch.Envelope) error {
	if err := s.Recover(ctx); err != nil {
		return err
	}
	if err := envelope.Validate(); err != nil {
		return err
	}
	a := s.adapters[envelope.Agent]
	if s.opts.Exclusions.ExcludesPath(a, envelope.Path) || s.excluded(envelope.Path) {
		return nil
	}
	existing, _, err := s.current("launch", envelope.Agent, envelope.ID)
	if err != nil {
		return err
	}
	if existing != nil {
		expected, err := jsonx.SortedCompact(existing.Launch.Envelope)
		if err != nil {
			return err
		}
		actual, err := jsonx.SortedCompact(envelope)
		if err != nil {
			return err
		}
		if !bytes.Equal(expected, actual) {
			return errors.New("archive launch: stable id has conflicting payload")
		}
		return nil
	}
	sf, ok, err := s.classifyHookPath(ctx, envelope.Agent, envelope.Path)
	if err != nil {
		return err
	}
	if !ok || sf.SessionKey == "" {
		return errors.New("archive launch: no session identity")
	}
	if s.excludedFile(sf) {
		return nil
	}
	cwd, _ := envelope.Payload["cwd"].(string)
	if cwd == "" {
		input, _ := envelope.Payload["hook_input"].(map[string]any)
		cwd, _ = input["cwd"].(string)
	}
	git := gitinfo.Read(ctx, cwd, s.cfg.MachineID)
	snap, bodies, dependencies, err := s.stageSnapshot(sf.Session(), sf.Root, cwd)
	if err != nil {
		return err
	}
	snap.Git = git
	snap.Timing = "captured"
	snap.TakenAt = s.nowSeconds()
	snap.CheckedAt = snap.TakenAt
	record := &launchRecord{Envelope: envelope, SessionKey: sf.SessionKey, Root: sf.Root, Git: git, Snapshot: snap}
	p := &publication{Schema: 3, MachineID: s.cfg.MachineID, Kind: "launch", Agent: envelope.Agent, Key: envelope.ID, Sequence: 1, Launch: record, Dependencies: dependencies}
	r, err := s.publish(ctx, p, bodies)
	if err != nil {
		return err
	}
	if err := s.applyLaunch(r, record); err != nil {
		return err
	}
	s.sessionRoots[sf.Session()] = sf.Root
	s.touched[sf.Session()] = true
	return nil
}

func (s *Syncer) applyLaunch(r state.Record, record *launchRecord) error {
	ref := adapters.SessionRef{Agent: record.Envelope.Agent, Key: record.SessionKey}
	sess, err := s.session(ref, record.Envelope.RecordedAt)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(sess.Launches, func(entry state.Launch) bool { return entry.Envelope.ID == record.Envelope.ID }) {
		sess.Launches = append(sess.Launches, *record)
	}
	slices.SortFunc(sess.Launches, func(a, b state.Launch) int {
		return cmp.Or(cmp.Compare(a.Envelope.RecordedAt, b.Envelope.RecordedAt), strings.Compare(a.Envelope.ID, b.Envelope.ID))
	})
	sess.FirstSeen = min(sess.FirstSeen, record.Envelope.RecordedAt)
	other := slices.DeleteFunc(slices.Clone(sess.Snapshots), func(snap state.SnapshotEntry) bool { return snap.Timing == "captured" })
	var captured []state.SnapshotEntry
	ordered := slices.Clone(sess.Launches)
	slices.SortFunc(ordered, func(a, b state.Launch) int {
		return cmp.Or(cmp.Compare(a.Snapshot.TakenAt, b.Snapshot.TakenAt), strings.Compare(a.Envelope.ID, b.Envelope.ID))
	})
	for i := range ordered {
		entry := &ordered[i]
		if entry.Snapshot.ID != "" {
			captured = addSnapshot(captured, entry.Snapshot, entry.Git, "captured", entry.Snapshot.TakenAt)
		}
	}
	snapshots, err := state.MergeSnapshots(other, captured)
	if err != nil {
		return err
	}
	sess.Snapshots = snapshots
	return s.db.CommitSession(r, ref, sess, state.SessionLaunch)
}

func (s *Syncer) timing(files []state.File) string {
	if strings.HasPrefix(s.opts.Trigger, "hook:") {
		return "in_session"
	}
	var latest int64
	for i := range files {
		f := &files[i]
		if f.Role == adapters.RoleMain {
			if f.MtimeNS == mtimeLastLineHeldBack {
				return "in_session"
			}
			latest = max(latest, f.MtimeNS)
		}
	}
	if s.nowSeconds()-float64(latest)/1e9 < inSessionWindow.Seconds() {
		return "in_session"
	}
	return "post_hoc"
}

func addSnapshot(snapshots []state.SnapshotEntry, snap state.SnapshotEntry, git gitinfo.State, timing string, now float64) []state.SnapshotEntry {
	if len(snapshots) > 0 {
		last := &snapshots[len(snapshots)-1]
		if last.ID == snap.ID && equalPtr(last.MemoryID, snap.MemoryID) && last.Git.Head == git.Head && last.Git.Branch == git.Branch {
			last.CheckedAt = now
			return snapshots
		}
	}
	snap.Git = git
	snap.Timing = timing
	snap.TakenAt = now
	snap.CheckedAt = now
	return append(snapshots, snap)
}

func equalPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (s *Syncer) session(ref adapters.SessionRef, now float64) (*state.Session, error) {
	sess, err := s.db.Session(ref)
	if sess == nil && err == nil {
		sess = &state.Session{Info: map[string]any{}, FirstSeen: now}
	}
	return sess, err
}

func orEmpty[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func (s *Syncer) writeManifests(ctx context.Context) error {
	refs := make([]adapters.SessionRef, 0, len(s.touched))
	for ref := range s.touched {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b adapters.SessionRef) int { return strings.Compare(a.String(), b.String()) })
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, bodies, sess, err := s.buildManifest(ctx, ref)
		if err != nil {
			s.errorf("manifest %s: %v", ref, err)
			continue
		}
		if p == nil {
			continue
		}
		r, err := s.publish(ctx, p, bodies)
		if err != nil {
			s.errorf("manifest %s: %v", ref, err)
			continue
		}
		if err := s.db.CommitSession(r, ref, sess, state.SessionManifestCurrent); err != nil {
			return err
		}
		s.Summary.Manifests++
	}
	return nil
}

type manifestDraft struct {
	ref         adapters.SessionRef
	root        string
	files       []state.File
	session     *state.Session
	publication *publication
	bodies      map[string][]byte
}

func (s *Syncer) buildManifest(ctx context.Context, ref adapters.SessionRef) (*publication, map[string][]byte, *state.Session, error) {
	draft, err := s.newManifestDraft(ref)
	if err != nil || draft == nil {
		return nil, nil, nil, err
	}
	if err := s.attachCommittedFiles(draft); err != nil {
		return nil, nil, nil, err
	}
	if err := s.attachCommittedLaunches(draft); err != nil {
		return nil, nil, nil, err
	}
	if err := s.captureManifestSnapshot(ctx, draft); err != nil {
		return nil, nil, nil, err
	}
	if err := s.attachSnapshotDependencies(ctx, draft); err != nil {
		return nil, nil, nil, err
	}
	changed, err := s.finalizeManifest(draft)
	if err != nil {
		return nil, nil, nil, err
	}
	if !changed {
		return nil, nil, nil, s.db.CacheSession(ref, draft.session)
	}
	return draft.publication, draft.bodies, draft.session, nil
}

func (s *Syncer) newManifestDraft(ref adapters.SessionRef) (*manifestDraft, error) {
	sess, err := s.session(ref, s.nowSeconds())
	if err != nil {
		return nil, err
	}
	root, _ := sess.Info["source_root"].(string)
	if known, ok := s.sessionRoots[ref]; ok {
		root = known
	}
	if s.opts.Exclusions.Excludes(adapters.SessionFile{Agent: ref.Agent, Root: root, SessionKey: ref.Key}) {
		return nil, nil
	}
	files, err := s.db.SessionFiles(ref)
	if err != nil {
		return nil, err
	}
	state.MergeFileInfo(sess.Info, files)
	if root != "" {
		sess.Info["source_root"] = root
	}
	m := &manifest{Schema: 3, Agent: ref.Agent, MachineID: s.cfg.MachineID, Hostname: s.cfg.Hostname, SessionKey: ref.Key, Info: sess.Info, Transcripts: []transcript{}, Launches: []launchRecord{}, Snapshots: orEmpty(slices.Clone(sess.Snapshots)), DeletedLocally: []deletion{}, FirstSeen: sess.FirstSeen, CollectorVersion: s.opts.Version}
	p := &publication{Schema: 3, MachineID: s.cfg.MachineID, Kind: "manifest", Agent: ref.Agent, Key: ref.Key, Sequence: 1, Manifest: m, Dependencies: []dependency{}}
	return &manifestDraft{ref: ref, root: root, files: files, session: sess, publication: p, bodies: map[string][]byte{}}, nil
}

func (s *Syncer) attachCommittedFiles(draft *manifestDraft) error {
	p := draft.publication
	m := p.Manifest
	for i := range draft.files {
		file, r, err := s.current("file", draft.ref.Agent, draft.files[i].Key)
		if err != nil {
			return err
		}
		if file == nil {
			continue
		}
		g := file.File
		versions, err := s.db.Versions(s.rawKey(p.Agent, file.Key))
		if err != nil {
			return err
		}
		m.Transcripts = append(m.Transcripts, transcript{Key: file.Key, Role: adapters.Role(g.Role), Size: g.Size, SHA256: g.SHA256, LineCount: g.LineCount, PrefixSHA256: g.PrefixSHA256, Head: g.Head, Object: r.Key, Pieces: g.Pieces, Versions: versions, Commit: r.Key, History: s.publicationPrefix("file", p.Agent, file.Key) + "commits/"})
		p.Dependencies = append(p.Dependencies, metadataDependency(r))
		if g.DeletedAt > 0 {
			m.DeletedLocally = append(m.DeletedLocally, deletion{Key: file.Key, At: g.DeletedAt})
		}
	}
	return nil
}

func metadataDependency(record *state.Record) dependency {
	return dependency{Object: record.Key, SHA256: record.ID, Size: int64(len(record.Body)), Encoding: "plain"}
}

func (s *Syncer) attachCommittedLaunches(draft *manifestDraft) error {
	p := draft.publication
	m := p.Manifest
	for i := range draft.session.Launches {
		record := &draft.session.Launches[i]
		_, r, err := s.current("launch", p.Agent, record.Envelope.ID)
		if err != nil {
			return err
		}
		if r == nil {
			return errors.New("archive manifest: launch has no committed record")
		}
		m.Launches = append(m.Launches, *record)
		p.Dependencies = append(p.Dependencies, metadataDependency(r))
	}
	if len(m.Launches) > 0 {
		m.Launch = &m.Launches[0]
		m.Git = m.Launch.Git
	} else if len(m.Snapshots) > 0 {
		m.Git = m.Snapshots[0].Git
	}
	return nil
}

func (s *Syncer) captureManifestSnapshot(ctx context.Context, draft *manifestDraft) error {
	sess := draft.session
	cwd, _ := sess.Info["cwd"].(string)
	if draft.root == "" || cwd == "" || len(sess.Snapshots) > 0 && s.nowSeconds()-sess.Snapshots[len(sess.Snapshots)-1].CheckedAt < snapshotRecheck.Seconds() {
		return nil
	}
	snap, bodies, deps, err := s.stageSnapshot(draft.ref, draft.root, cwd)
	if err != nil {
		return err
	}
	git := gitinfo.Read(ctx, cwd, s.cfg.MachineID)
	sess.Snapshots = addSnapshot(sess.Snapshots, snap, git, s.timing(draft.files), s.nowSeconds())
	m := draft.publication.Manifest
	m.Snapshots = slices.Clone(sess.Snapshots)
	maps.Copy(draft.bodies, bodies)
	draft.publication.Dependencies = append(draft.publication.Dependencies, deps...)
	if m.Git.IsZero() {
		m.Git = git
	}
	return nil
}

func (s *Syncer) attachSnapshotDependencies(ctx context.Context, draft *manifestDraft) error {
	p := draft.publication
	m := p.Manifest
	for i := range m.Snapshots {
		m.Snapshots[i].CheckedAt = 0
		for _, id := range snapshotIDs(m.Snapshots[i]) {
			deps, err := s.snapshotDependencies(ctx, id, draft.bodies, p.Agent)
			if err != nil {
				return err
			}
			p.Dependencies = append(p.Dependencies, deps...)
		}
	}
	p.Dependencies = uniqueDependencies(p.Dependencies)
	return nil
}

func (s *Syncer) finalizeManifest(draft *manifestDraft) (bool, error) {
	p := draft.publication
	m := p.Manifest
	previous, r, err := s.current("manifest", p.Agent, p.Key)
	if err != nil {
		return false, err
	}
	if previous != nil {
		old := *previous.Manifest
		old.SyncedAt = 0
		before, err := jsonx.SortedCompact(old)
		if err != nil {
			return false, err
		}
		after, err := jsonx.SortedCompact(m)
		if err != nil {
			return false, err
		}
		if bytes.Equal(before, after) {
			return false, nil
		}
		p.Sequence = r.Sequence + 1
		p.Parent = r.ID
	}
	m.SyncedAt = s.nowSeconds()
	return true, nil
}

func snapshotIDs(snap state.SnapshotEntry) []string {
	ids := []string{}
	if snap.ID != "" {
		ids = append(ids, snap.ID)
	}
	if snap.MemoryID != nil {
		ids = append(ids, *snap.MemoryID)
	}
	return ids
}
func snapshotKeyFor(id string) string { return "v3/snapshots/sets/" + id + ".json" }
func plainKey(sha string) string      { return "v3/blobs/plain/" + sha }

func (s *Syncer) stageSnapshot(ref adapters.SessionRef, root, cwd string) (state.SnapshotEntry, map[string][]byte, []dependency, error) {
	if !utf8.ValidString(root) || !utf8.ValidString(cwd) {
		return state.SnapshotEntry{}, nil, nil, errors.New("archive snapshot: unsupported non-UTF-8 path")
	}
	if cwd == "" {
		return state.SnapshotEntry{}, map[string][]byte{}, nil, nil
	}
	snap, err := snapshot.Build(s.adapters[ref.Agent], root, cwd, ref.Key)
	if err != nil {
		return state.SnapshotEntry{}, nil, nil, err
	}
	bodies := map[string][]byte{}
	var dependencies []dependency
	for sha, body := range snap.Blobs {
		key := plainKey(sha)
		bodies[key] = body
		dependencies = append(dependencies, dependency{Object: key, SHA256: sha, Size: int64(len(body)), Encoding: "plain"})
	}
	sets := []*snapshot.Set{snap.Harness}
	if snap.Memory != nil {
		sets = append(sets, snap.Memory)
	}
	for _, set := range sets {
		s.snapshotSkips(set)
		if err := validateSnapshotPaths(set); err != nil {
			return state.SnapshotEntry{}, nil, nil, err
		}
		body, err := jsonx.SortedCompact(set)
		if err != nil {
			return state.SnapshotEntry{}, nil, nil, err
		}
		key := snapshotKeyFor(set.ID)
		bodies[key] = body
		dependencies = append(dependencies, dependency{Object: key, SHA256: set.ID, Size: int64(len(body)), Encoding: "plain"})
	}
	entry := state.SnapshotEntry{ID: snap.Harness.ID, Mtimes: snap.Mtimes}
	if snap.Memory != nil {
		entry.MemoryID = &snap.Memory.ID
	}
	return entry, bodies, dependencies, nil
}

// Snapshot labels are filesystem identities too: do not silently rename Unix byte paths.
func validateSnapshotPaths(set *snapshot.Set) error {
	paths := []string{set.UserBase}
	if set.ProjectRoot != nil {
		paths = append(paths, *set.ProjectRoot)
	}
	for _, file := range set.Files {
		paths = append(paths, file.Path, file.Target)
	}
	for _, skipped := range set.Skipped {
		paths = append(paths, skipped.Path)
	}
	for _, path := range paths {
		if !utf8.ValidString(path) {
			return errors.New("archive snapshot: unsupported non-UTF-8 source path")
		}
	}
	return nil
}

func (s *Syncer) snapshotDependencies(ctx context.Context, id string, bodies map[string][]byte, agent string) ([]dependency, error) {
	key := snapshotKeyFor(id)
	body, ok := bodies[key]
	if !ok {
		var err error
		body, err = s.readRecord(ctx, key)
		if err != nil {
			return nil, err
		}
	}
	if raw.SHA256(body) != id {
		return nil, errors.New("archive snapshot: set checksum mismatch")
	}
	var set snapshot.Set
	if err := jsonx.Decode(body, &set); err != nil {
		return nil, err
	}
	if set.Agent != agent || (set.Kind != "harness" && set.Kind != "memory") {
		return nil, errors.New("archive snapshot: invalid set identity")
	}
	deps := []dependency{{Object: key, SHA256: id, Size: int64(len(body)), Encoding: "plain"}}
	for _, file := range set.Files {
		if !raw.ValidHash(file.SHA256) || file.Size < 0 {
			return nil, errors.New("archive snapshot: invalid file reference")
		}
		deps = append(deps, dependency{Object: plainKey(file.SHA256), SHA256: file.SHA256, Size: int64(file.Size), Encoding: "plain"})
	}
	return deps, nil
}

func uniqueDependencies(deps []dependency) []dependency {
	slices.SortFunc(deps, func(a, b dependency) int { return strings.Compare(a.Object, b.Object) })
	return slices.Compact(deps)
}

func (s *Syncer) publish(ctx context.Context, p *publication, bodies map[string][]byte) (state.Record, error) {
	// Nonfile dependencies are a set, including launch snapshot bodies collected from a map.
	// File dependencies instead follow decoded piece order and must not be reordered.
	if p.Kind != "file" {
		p.Dependencies = uniqueDependencies(p.Dependencies)
	}
	if err := p.validate(s.cfg.MachineID); err != nil {
		return state.Record{}, err
	}
	if err := s.verifyReferences(ctx, p, bodies); err != nil {
		return state.Record{}, err
	}
	body, id, err := canonicalPublication(p)
	if err != nil {
		return state.Record{}, err
	}
	if err := s.putRecord(ctx, s.publicationKey(p, false, id), body); err != nil {
		return state.Record{}, err
	}
	for _, dependency := range p.Dependencies {
		if err := s.ensureDependency(ctx, dependency, bodies); err != nil {
			return state.Record{}, err
		}
	}
	if err := s.verifyWriter(ctx); err != nil {
		return state.Record{}, err
	}
	key := s.publicationKey(p, true, id)
	if err := s.putRecord(ctx, key, body); err != nil {
		return state.Record{}, err
	}
	return recordFor(key, id, p, body, true), nil
}

func (s *Syncer) ensureDependency(ctx context.Context, d dependency, bodies map[string][]byte) error {
	if body, ok := bodies[d.Object]; ok {
		_, err := s.bucket.Head(ctx, d.Object)
		if errors.Is(err, store.ErrNotFound) {
			err = s.bucket.Put(ctx, d.Object, body, map[string]string{"sha256": d.SHA256})
		}
		if err != nil {
			if verify := verifyDependency(ctx, s.bucket, d); verify != nil {
				return errors.Join(err, verify)
			}
		}
	}
	return verifyDependencyMetadata(ctx, s.bucket, d)
}

// snapshotSkips makes deliberate snapshot privacy omissions visible in run history and doctor,
// including project/import files that are not part of raw discovery.
func (s *Syncer) snapshotSkips(set *snapshot.Set) {
	for _, skipped := range set.Skipped {
		switch skipped.Reason {
		case "literal_secret_assignment", "symlink_directory":
			s.skipReason(skipped.Reason)
		}
	}
}
