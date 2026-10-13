package syncer

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/gitinfo"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/snapshot"
	"github.com/mich-murphy/memory/internal/state"
)

func (s *Syncer) RecordLaunch(ctx context.Context, agent, path string, launch map[string]any) error {
	sf, ok, err := s.classifyHookPath(agent, path)
	if err != nil || !ok || sf.SessionKey == "" {
		return err
	}
	cwd, _ := launch["cwd"].(string)
	if cwd == "" {
		if hi, ok := launch["hook_input"].(map[string]any); ok {
			cwd, _ = hi["cwd"].(string)
		}
	}
	ref := sf.Session()
	now := s.nowSeconds()
	sess, err := s.session(ref, now)
	if err != nil {
		return err
	}
	if _, ok := sess.Info["cwd"]; !ok && cwd != "" {
		sess.Info["cwd"] = cwd
	}
	git := gitinfo.Read(ctx, cwd, s.cfg.MachineID)
	entry := maps.Clone(launch)
	entry["git"] = git
	snap, err := s.takeSnapshot(ctx, ref, sf.Root, cwd)
	if err != nil {
		return err
	}
	entry["snapshot"] = snap.ID
	sess.Snapshots = addSnapshot(sess.Snapshots, snap, git, "pre_session", now)
	sess.Launches = append(sess.Launches, entry)
	if err := s.db.PutSession(ref, sess); err != nil {
		return err
	}
	if err := s.db.Flush(); err != nil {
		return err
	}
	s.sessionRoots[ref] = sf.Root
	return s.markManifestPending(ref)
}

func (s *Syncer) timing(files []state.File) string {
	if strings.HasPrefix(s.opts.Trigger, "hook:") {
		return "in_session"
	}
	var latest int64
	for i := range files {
		f := &files[i]
		if f.Role != adapters.RoleMain {
			continue
		}
		if f.MtimeNS == mtimeLastLineHeldBack {
			return "in_session"
		}
		latest = max(latest, f.MtimeNS)
	}
	if s.nowSeconds()-float64(latest)/1e9 < inSessionWindow.Seconds() {
		return "in_session"
	}
	return "post_hoc"
}

func addSnapshot(snapshots []state.SnapshotEntry, snap state.SnapshotEntry, git gitinfo.State, timing string,
	now float64,
) []state.SnapshotEntry {
	if n := len(snapshots); n > 0 {
		last := &snapshots[n-1]
		sameGit := last.Git.Head == git.Head && last.Git.Branch == git.Branch
		if last.ID == snap.ID && equalPtr(last.MemoryID, snap.MemoryID) && sameGit {
			last.CheckedAt = now
			return snapshots
		}
	}
	snap.Git, snap.Timing, snap.TakenAt, snap.CheckedAt = git, timing, now, now
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

type manifestJob struct {
	ref  adapters.SessionRef
	key  string
	body []byte
	sha  string
}

func (s *Syncer) writeManifests(ctx context.Context) error {
	refs := slices.SortedFunc(maps.Keys(s.touched), func(a, b adapters.SessionRef) int {
		return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Key, b.Key))
	})
	var jobs []manifestJob
	for _, ref := range refs {
		job, err := s.buildManifest(ctx, ref)
		if err != nil {
			s.errorf("manifest %s: %v", ref, err)
			continue
		}
		if job != nil {
			jobs = append(jobs, *job)
		} else if err := s.db.ManifestWritten(ref); err != nil {
			return err
		}
	}
	errs := parallel(jobs, func(j manifestJob) error {
		return s.bucket.Put(ctx, j.key, j.body, map[string]string{"sha256": j.sha})
	})
	for i, j := range jobs {
		if errs[i] != nil {
			s.errorf("manifest %s: %v", j.ref, errs[i])
			continue
		}
		if err := s.db.RecordUpload(j.key, j.sha, int64(len(j.body))); err != nil {
			return err
		}
		if err := s.db.ManifestWritten(j.ref); err != nil {
			return err
		}
		s.Summary.Manifests++
	}
	return nil
}

func (s *Syncer) maybeSnapshot(ctx context.Context, ref adapters.SessionRef, sess *state.Session, files []state.File,
	now float64,
) error {
	root, ok := s.sessionRoots[ref]
	cwd, _ := sess.Info["cwd"].(string)
	if !ok || cwd == "" {
		return nil
	}
	if n := len(sess.Snapshots); n > 0 && now-sess.Snapshots[n-1].CheckedAt < snapshotRecheck.Seconds() {
		return nil
	}
	snap, err := s.takeSnapshot(ctx, ref, root, cwd)
	if err != nil {
		return err
	}
	git := gitinfo.Read(ctx, cwd, s.cfg.MachineID)
	sess.Snapshots = addSnapshot(sess.Snapshots, snap, git, s.timing(files), now)
	return nil
}

func (s *Syncer) buildManifest(ctx context.Context, ref adapters.SessionRef) (*manifestJob, error) {
	now := s.nowSeconds()
	sess, err := s.session(ref, now)
	if err != nil {
		return nil, err
	}
	files, err := s.db.SessionFiles(ref)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if f := &files[i]; f.Role == adapters.RoleMain && len(f.Head) > 0 {
			maps.Copy(sess.Info, f.Head)
		}
	}
	if root, ok := s.sessionRoots[ref]; ok {
		sess.Info["source_root"] = root
	}
	if err := s.maybeSnapshot(ctx, ref, sess, files, now); err != nil {
		return nil, err
	}
	if err := s.db.PutSession(ref, sess); err != nil {
		return nil, err
	}
	manifest, err := s.manifest(ref, sess, files)
	if err != nil {
		return nil, err
	}
	body, err := jsonx.SortedCompact(manifest)
	if err != nil {
		return nil, err
	}
	sha := raw.SHA256(body)
	key := s.manifestKey(ref)
	if prev, err := s.db.UploadedSHA(key); err != nil || prev == sha {
		return nil, err
	}
	manifest["synced_at"] = now
	manifest["collector_version"] = s.opts.Version
	body, err = jsonx.SortedIndented(manifest)
	if err != nil {
		return nil, err
	}
	return &manifestJob{ref: ref, key: key, body: body, sha: sha}, nil
}

func (s *Syncer) manifest(ref adapters.SessionRef, sess *state.Session, files []state.File) (map[string]any, error) {
	transcripts := make([]map[string]any, 0, len(files))
	var deleted []map[string]any
	for i := range files {
		f := &files[i]
		obj := s.rawKey(ref.Agent, f.Key)
		pieces, err := s.db.Pieces(obj)
		if err != nil {
			return nil, err
		}
		versions, err := s.db.Versions(obj)
		if err != nil {
			return nil, err
		}
		transcripts = append(transcripts, map[string]any{
			"key": f.Key, "role": f.Role, "size": f.Size, "sha256": f.SHA256, "line_count": f.LineCount,
			"prefix_sha256": nilIfEmpty(f.PrefixSHA256), "head": f.Head, "object": obj, "pieces": pieces,
			"versions": versions,
		})
		if f.DeletedAt > 0 {
			deleted = append(deleted, map[string]any{"key": f.Key, "at": f.DeletedAt})
		}
	}
	snapshots := make([]state.SnapshotEntry, len(sess.Snapshots))
	copy(snapshots, sess.Snapshots)
	for i := range snapshots {
		snapshots[i].CheckedAt = 0 // left out of the manifest so a recheck alone doesn't rewrite it
	}
	var launch map[string]any
	if len(sess.Launches) > 0 {
		launch = sess.Launches[0]
	}
	var git any = map[string]any{}
	if g, _ := launch["git"].(map[string]any); len(g) > 0 {
		git = g
	} else if len(sess.Snapshots) > 0 && !sess.Snapshots[0].Git.IsZero() {
		git = sess.Snapshots[0].Git
	}
	return map[string]any{
		"schema":          2,
		"agent":           ref.Agent,
		"machine_id":      s.cfg.MachineID,
		"hostname":        s.cfg.Hostname,
		"session_key":     ref.Key,
		"info":            sess.Info,
		"transcripts":     transcripts,
		"launch":          launch,
		"launches":        orEmpty(sess.Launches),
		"git":             git,
		"snapshots":       snapshots,
		"deleted_locally": orEmpty(deleted),
		"first_seen":      sess.FirstSeen,
	}, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (s *Syncer) takeSnapshot(ctx context.Context, ref adapters.SessionRef, root, cwd string) (state.SnapshotEntry, error) {
	ck := snapshotKey{session: ref.Agent, root: root, cwd: cwd}
	if ref.Agent == "claude" {
		ck.session = ref.String()
	}
	if r, ok := s.snapshotCache[ck]; ok {
		return r, nil
	}
	snap, err := snapshot.Build(s.adapters[ref.Agent], root, cwd, ref.Key)
	if err != nil {
		return state.SnapshotEntry{}, err
	}
	var missing []string
	for _, sha := range slices.Sorted(maps.Keys(snap.Blobs)) {
		have, err := s.db.UploadedSHA("snapshots/blobs/" + sha)
		if err != nil {
			return state.SnapshotEntry{}, err
		}
		if have == "" {
			missing = append(missing, sha)
		}
	}
	errs := parallel(missing, func(sha string) error {
		return s.bucket.Put(ctx, "snapshots/blobs/"+sha, snap.Blobs[sha], map[string]string{"sha256": sha})
	})
	for i, sha := range missing {
		if errs[i] != nil {
			return state.SnapshotEntry{}, errs[i]
		}
		if err := s.db.RecordUpload("snapshots/blobs/"+sha, sha, int64(len(snap.Blobs[sha]))); err != nil {
			return state.SnapshotEntry{}, err
		}
	}
	if err := s.putSet(ctx, snap.Harness); err != nil {
		return state.SnapshotEntry{}, err
	}
	r := state.SnapshotEntry{ID: snap.Harness.ID, Mtimes: snap.Mtimes}
	if snap.Memory != nil {
		if err := s.putSet(ctx, snap.Memory); err != nil {
			return state.SnapshotEntry{}, err
		}
		r.MemoryID = &snap.Memory.ID
	}
	s.snapshotCache[ck] = r
	return r, nil
}

func (s *Syncer) putSet(ctx context.Context, set *snapshot.Set) error {
	key := "snapshots/sets/" + set.ID + ".json"
	have, err := s.db.UploadedSHA(key)
	if err != nil || have != "" {
		return err
	}
	body, err := jsonx.SortedIndented(set)
	if err != nil {
		return err
	}
	if err := s.bucket.Put(ctx, key, body, map[string]string{}); err != nil {
		return err
	}
	return s.db.RecordUpload(key, set.ID, int64(len(body)))
}
