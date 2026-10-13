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
	sf, root, ok, err := s.classifyHookPath(agent, path)
	if err != nil || !ok || sf.SessionKey == "" {
		return err
	}
	cwd, _ := launch["cwd"].(string)
	if cwd == "" {
		if hi, ok := launch["hook_input"].(map[string]any); ok {
			cwd, _ = hi["cwd"].(string)
		}
	}
	now := s.nowSeconds()
	sess, err := s.state.GetSession(agent, sf.SessionKey)
	if err != nil {
		return err
	}
	if sess == nil {
		sess = &state.Session{Info: map[string]any{}, FirstSeen: now}
	}
	if _, ok := sess.Info["cwd"]; !ok && cwd != "" {
		sess.Info["cwd"] = cwd
	}
	git := gitinfo.State(ctx, cwd, s.cfg.MachineID)
	entry := maps.Clone(launch)
	entry["git"] = git
	if s.opts.Snapshots {
		snap, err := s.takeSnapshot(ctx, agent, root, cwd, sf.SessionKey)
		if err != nil {
			return err
		}
		entry["snapshot"] = snap.setID
		sess.Snapshots = addSnapshot(sess.Snapshots, snap, git, "pre_session", now)
		s.exportGit(ctx, git)
	}
	sess.Launches = append(sess.Launches, entry)
	if err := s.state.PutSession(agent, sf.SessionKey, sess); err != nil {
		return err
	}
	if err := s.state.Flush(); err != nil {
		return err
	}
	id := sessionID{agent, sf.SessionKey}
	s.sessionRoots[id] = root
	return s.markManifestPending(id)
}

func (s *Syncer) timing(files []state.File) string {
	if strings.HasPrefix(s.opts.Trigger, "hook:") {
		return "in_session"
	}
	var latest int64
	for i := range files {
		f := &files[i]
		if f.Role != string(adapters.RoleMain) {
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

func addSnapshot(snapshots []state.SnapshotEntry, snap snapshotResult, git state.GitState, timing string,
	now float64,
) []state.SnapshotEntry {
	if n := len(snapshots); n > 0 {
		last := &snapshots[n-1]
		sameGit := last.Git.Head == git.Head && last.Git.Branch == git.Branch
		if last.ID == snap.setID && equalPtr(last.MemoryID, snap.memoryID) && sameGit {
			last.CheckedAt = now
			return snapshots
		}
	}
	return append(snapshots, state.SnapshotEntry{
		ID: snap.setID, MemoryID: snap.memoryID, Git: git, Timing: timing, TakenAt: now, CheckedAt: now,
		Mtimes: snap.mtimes,
	})
}

func equalPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type manifestJob struct {
	id   sessionID
	key  string
	body []byte
	sha  string
}

func (s *Syncer) writeManifests(ctx context.Context) error {
	ids := slices.SortedFunc(maps.Keys(s.touched), func(a, b sessionID) int {
		return cmp.Or(cmp.Compare(a.agent, b.agent), cmp.Compare(a.key, b.key))
	})
	var jobs []manifestJob
	for _, id := range ids {
		job, err := s.buildManifest(ctx, id.agent, id.key)
		if err != nil {
			s.errorf("manifest %s/%s: %v", id.agent, id.key, err)
			continue
		}
		if job != nil {
			jobs = append(jobs, *job)
		} else if err := s.state.ManifestWritten(id.agent, id.key); err != nil {
			return err
		}
	}
	errs := parallel(jobs, func(j manifestJob) error {
		return s.store.Put(ctx, j.key, j.body, map[string]string{"sha256": j.sha})
	})
	for i, j := range jobs {
		if errs[i] != nil {
			s.errorf("manifest %s/%s: %v", j.id.agent, j.id.key, errs[i])
			continue
		}
		if err := s.state.RecordUpload(j.key, j.sha, nil); err != nil {
			return err
		}
		if err := s.state.ManifestWritten(j.id.agent, j.id.key); err != nil {
			return err
		}
		s.Summary.Manifests++
	}
	return nil
}

func (s *Syncer) maybeSnapshot(ctx context.Context, agent, sessionKey string, sess *state.Session, files []state.File,
	now float64,
) error {
	root, ok := s.sessionRoots[sessionID{agent, sessionKey}]
	cwd, _ := sess.Info["cwd"].(string)
	if !s.opts.Snapshots || !ok || cwd == "" {
		return nil
	}
	if n := len(sess.Snapshots); n > 0 && now-sess.Snapshots[n-1].CheckedAt < snapshotRecheck.Seconds() {
		return nil
	}
	snap, err := s.takeSnapshot(ctx, agent, root, cwd, sessionKey)
	if err != nil {
		return err
	}
	git := gitinfo.State(ctx, cwd, s.cfg.MachineID)
	sess.Snapshots = addSnapshot(sess.Snapshots, snap, git, s.timing(files), now)
	s.exportGit(ctx, git)
	return nil
}

func (s *Syncer) buildManifest(ctx context.Context, agent, sessionKey string) (*manifestJob, error) {
	now := s.nowSeconds()
	sess, err := s.state.GetSession(agent, sessionKey)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		sess = &state.Session{Info: map[string]any{}, FirstSeen: now}
	}
	files, err := s.state.SessionFiles(agent, sessionKey)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if f := &files[i]; f.Role == string(adapters.RoleMain) && len(f.Head) > 0 {
			maps.Copy(sess.Info, f.Head)
		}
	}
	if root, ok := s.sessionRoots[sessionID{agent, sessionKey}]; ok {
		sess.Info["source_root"] = root
	}
	if err := s.maybeSnapshot(ctx, agent, sessionKey, sess, files, now); err != nil {
		return nil, err
	}
	if err := s.state.PutSession(agent, sessionKey, sess); err != nil {
		return nil, err
	}
	manifest, err := s.manifest(agent, sessionKey, sess, files)
	if err != nil {
		return nil, err
	}
	body, err := jsonx.SortedCompact(manifest)
	if err != nil {
		return nil, err
	}
	sha := raw.SHA256(body)
	key := s.manifestKey(agent, sessionKey)
	if prev, err := s.state.UploadedSHA(key); err != nil || prev == sha {
		return nil, err
	}
	manifest["synced_at"] = now
	manifest["collector_version"] = s.opts.Version
	body, err = jsonx.SortedIndented(manifest)
	if err != nil {
		return nil, err
	}
	return &manifestJob{id: sessionID{agent, sessionKey}, key: key, body: body, sha: sha}, nil
}

func (s *Syncer) manifest(agent, sessionKey string, sess *state.Session, files []state.File) (map[string]any, error) {
	transcripts := make([]map[string]any, 0, len(files))
	var deleted []map[string]any
	for i := range files {
		f := &files[i]
		obj := s.rawKey(agent, f.Key)
		pieces, err := s.state.Pieces(obj)
		if err != nil {
			return nil, err
		}
		versions, err := s.state.Versions(obj)
		if err != nil {
			return nil, err
		}
		transcripts = append(transcripts, map[string]any{
			"key": f.Key, "role": f.Role, "size": f.Size, "sha256": f.SHA256, "line_count": f.LineCount,
			"prefix_sha256": nilIfEmpty(f.PrefixSHA256), "head": f.Head, "object": obj, "pieces": pieces,
			"versions": versions,
		})
		if f.DeletedAt != nil {
			deleted = append(deleted, map[string]any{"key": f.Key, "at": *f.DeletedAt})
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
		"agent":           agent,
		"machine_id":      s.cfg.MachineID,
		"hostname":        s.cfg.Hostname,
		"session_key":     sessionKey,
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

func (s *Syncer) takeSnapshot(ctx context.Context, agent, root, cwd, sessionKey string) (snapshotResult, error) {
	ck := snapshotKey{agent: agent, root: root, cwd: cwd}
	if agent == "claude" {
		ck.session = sessionKey
	}
	if r, ok := s.snapshotCache[ck]; ok {
		return r, nil
	}
	snap, err := snapshot.Build(s.adapters[agent], root, cwd, sessionKey)
	if err != nil {
		return snapshotResult{}, err
	}
	var missing []string
	for _, sha := range slices.Sorted(maps.Keys(snap.Blobs)) {
		have, err := s.state.UploadedSHA("snapshots/blobs/" + sha)
		if err != nil {
			return snapshotResult{}, err
		}
		if have == "" {
			missing = append(missing, sha)
		}
	}
	errs := parallel(missing, func(sha string) error {
		return s.store.Put(ctx, "snapshots/blobs/"+sha, snap.Blobs[sha], map[string]string{"sha256": sha})
	})
	for i, sha := range missing {
		if errs[i] != nil {
			return snapshotResult{}, errs[i]
		}
		if err := s.state.RecordUpload("snapshots/blobs/"+sha, sha, nil); err != nil {
			return snapshotResult{}, err
		}
	}
	if err := s.putSet(ctx, snap.SetID, snap.Doc); err != nil {
		return snapshotResult{}, err
	}
	r := snapshotResult{setID: snap.SetID, mtimes: snap.Mtimes}
	if snap.MemoryID != "" {
		if err := s.putSet(ctx, snap.MemoryID, snap.MemoryDoc); err != nil {
			return snapshotResult{}, err
		}
		r.memoryID = &snap.MemoryID
	}
	s.snapshotCache[ck] = r
	return r, nil
}

func (s *Syncer) putSet(ctx context.Context, setID string, doc map[string]any) error {
	key := "snapshots/sets/" + setID + ".json"
	have, err := s.state.UploadedSHA(key)
	if err != nil || have != "" {
		return err
	}
	body, err := jsonx.SortedIndented(doc)
	if err != nil {
		return err
	}
	if err := s.store.Put(ctx, key, body, map[string]string{}); err != nil {
		return err
	}
	return s.state.RecordUpload(key, setID, nil)
}

func (s *Syncer) exportGit(ctx context.Context, git state.GitState) {
	if git.RepoID == "" || git.Head == "" {
		return
	}
	done, err := s.state.GitExported(git.RepoID, git.Head)
	if err != nil || done {
		return
	}
	heads, err := s.state.GitHeads(git.RepoID)
	if err != nil {
		s.errorf("git export %s: %v", git.Root, err)
		return
	}
	objects, err := gitinfo.Export(ctx, git, heads)
	if err == nil {
		for _, o := range objects {
			if err = s.store.Put(ctx, o.Key, o.Body, map[string]string{}); err != nil {
				break
			}
		}
	}
	if err == nil && len(objects) > 0 {
		err = s.state.RecordGitExport(git.RepoID, git.Head)
	}
	if err == nil {
		err = s.state.Flush()
	}
	if err != nil {
		s.errorf("git export %s: %v", git.Root, err)
	}
}
