package state

import (
	"bytes"
	"cmp"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
)

// Binding prevents disposable cache evidence from being reused for another archive.
// Destination is canonical and nonsecret; StoreID detects a replaced destination.
type Binding struct {
	Schema      int
	Destination string
	StoreID     string
	MachineID   string
}

func (s *State) Bind(want Binding) error {
	var got Binding
	found, err := s.queryRow("SELECT schema,destination,store_id,machine_id FROM binding WHERE singleton=1", nil, &got.Schema, &got.Destination, &got.StoreID, &got.MachineID)
	if err != nil {
		return err
	}
	if found {
		if got != want {
			return errors.New("state binding: incompatible format, destination, store identity or machine id; use a separate state directory")
		}
		return nil
	}
	if want.Schema != 3 || want.Destination == "" || want.StoreID == "" || want.MachineID == "" {
		return errors.New("state binding: invalid identity")
	}
	return s.Atomic(func() error {
		// Unbound v2 evidence is not evidence of upload into v3. Keep roots and run summaries,
		// but drop disposable upload/session projections on the explicit format transition.
		for _, table := range []string{"observations", "files", "uploads", "resumes", "pieces", "strays", "raw_versions", "sessions", "pending_manifests"} {
			if err := s.exec("DELETE FROM " + table); err != nil {
				return err
			}
		}
		if err := s.exec("DROP TABLE files"); err != nil {
			return err
		}
		if err := s.exec(schema); err != nil {
			return err
		}
		return s.exec("INSERT INTO binding VALUES(1,?,?,?,?)", want.Schema, want.Destination, want.StoreID, want.MachineID)
	})
}

// Atomic commits one domain operation or rolls it back in its entirety. Callbacks must
// not call Flush: individual record methods never decide a domain's commit boundary.
func (s *State) Atomic(apply func() error) error {
	if s.inTx {
		return errors.New("state atomic: unfinished domain operation")
	}
	if _, err := s.conn.ExecContext(s.ctx, "BEGIN"); err != nil {
		return fmt.Errorf("state atomic: %w", err)
	}
	s.inTx = true
	if err := apply(); err != nil {
		return errors.Join(err, s.Rollback())
	}
	return s.Flush()
}

// Rollback is also Close's behaviour: Close never silently commits partial records.
func (s *State) Rollback() error {
	if !s.inTx {
		return nil
	}
	s.inTx = false
	_, err := s.conn.ExecContext(s.ctx, "ROLLBACK")
	if err != nil {
		return fmt.Errorf("state rollback: %w", err)
	}
	return nil
}

type Record struct {
	Key       string
	Kind      string
	Stream    string
	ID        string
	Parent    string
	Sequence  uint64
	Committed bool
	Body      []byte
}

// RecordArchive imports validated immutable remote evidence idempotently. Divergent bytes
// at one immutable key are corruption, never an upsert to be silently accepted.
func (s *State) RecordArchive(record Record) error {
	var body []byte
	found, err := s.queryRow("SELECT body FROM archive_records WHERE key=?", []any{record.Key}, &body)
	if err != nil {
		return err
	}
	if found {
		if !bytes.Equal(body, record.Body) {
			return fmt.Errorf("state archive: conflicting immutable record %s", record.Key)
		}
		return nil
	}
	return s.exec("INSERT INTO archive_records VALUES(?,?,?,?,?,?,?,?)", record.Key, record.Kind, record.Stream, record.ID, record.Parent, record.Sequence, record.Committed, record.Body)
}

func (s *State) ArchiveRecords(kind, stream string) ([]Record, error) {
	return queryAll(s, "SELECT key,kind,stream,id,parent,sequence,committed,body FROM archive_records WHERE kind=? AND stream=? ORDER BY sequence,key", []any{kind, stream}, func(rows *sql.Rows, r *Record) error {
		return rows.Scan(&r.Key, &r.Kind, &r.Stream, &r.ID, &r.Parent, &r.Sequence, &r.Committed, &r.Body)
	})
}

// CurrentArchive returns a unique validated chain tip, rejecting all branch conflicts.
// Timestamps and response order cannot select a winner. Plans do not select current data.
func (s *State) CurrentArchive(kind, stream string) (*Record, error) {
	var count, maxSequence, distinct int64
	_, err := s.queryRow("SELECT COUNT(*),COALESCE(MAX(sequence),0),COUNT(DISTINCT sequence) FROM archive_records WHERE kind=? AND stream=? AND committed=1", []any{kind, stream}, &count, &maxSequence, &distinct)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	if count != maxSequence || distinct != count {
		return nil, fmt.Errorf("state archive: divergent or incomplete chain for %s", stream)
	}
	var invalid int
	_, err = s.queryRow("SELECT COUNT(*) FROM archive_records AS a WHERE a.kind=? AND a.stream=? AND a.committed=1 AND ((a.sequence=1 AND a.parent<>'') OR (a.sequence>1 AND NOT EXISTS (SELECT 1 FROM archive_records AS b WHERE b.kind=a.kind AND b.stream=a.stream AND b.committed=1 AND b.sequence=a.sequence-1 AND b.id=a.parent)))", []any{kind, stream}, &invalid)
	if err != nil {
		return nil, err
	}
	if invalid != 0 {
		return nil, fmt.Errorf("state archive: missing or conflicting parent for %s", stream)
	}
	var record Record
	_, err = s.queryRow("SELECT key,kind,stream,id,parent,sequence,committed,body FROM archive_records WHERE kind=? AND stream=? AND committed=1 ORDER BY sequence DESC LIMIT 1", []any{kind, stream}, &record.Key, &record.Kind, &record.Stream, &record.ID, &record.Parent, &record.Sequence, &record.Committed, &record.Body)
	return &record, err
}

func (s *State) ArchiveRecord(key string) (*Record, error) {
	var r Record
	found, err := s.queryRow("SELECT key,kind,stream,id,parent,sequence,committed,body FROM archive_records WHERE key=?", []any{key}, &r.Key, &r.Kind, &r.Stream, &r.ID, &r.Parent, &r.Sequence, &r.Committed, &r.Body)
	if !found || err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *State) ArchiveStreams(kind string) ([]string, error) {
	return queryAll(s, "SELECT DISTINCT stream FROM archive_records WHERE kind=? AND committed=1 ORDER BY stream", []any{kind}, func(rows *sql.Rows, r *string) error { return rows.Scan(r) })
}

// CommitFile atomically imports remote commit evidence and advances all local projections.
// It can be replayed after cache loss; original source paths, not staging paths, are recorded.
func (s *State) CommitFile(record Record, key string, g, previous *raw.Generation) error {
	return s.commitFile(record, key, g, previous, nil)
}

// CommitCapture includes the stat observation in the same publication-completion domain.
func (s *State) CommitCapture(record Record, key string, g, previous *raw.Generation, file *File) error {
	return s.commitFile(record, key, g, previous, file)
}

func (s *State) commitFile(record Record, key string, g, previous *raw.Generation, file *File) error {
	return s.Atomic(func() error {
		if err := s.RecordArchive(record); err != nil {
			return err
		}
		if err := s.projectGeneration(key, g, previous); err != nil {
			return err
		}
		if file != nil {
			if err := s.RecordFile(file); err != nil {
				return err
			}
			if err := s.recordObservation(key, file); err != nil {
				return err
			}
		}
		return s.MarkArchiveApplied(record.Key)
	})
}

func (s *State) projectGeneration(key string, g, previous *raw.Generation) error {
	if previous != nil && previous.SHA256 != g.SHA256 {
		version := Version{SHA256: previous.SHA256, Size: previous.Size, Pieces: previous.Pieces, VersionedAt: g.ObservedAt}
		if err := s.RecordVersion(key, &version); err != nil {
			return err
		}
	}
	if err := s.RecordUpload(key, g.SHA256, g.Size); err != nil {
		return err
	}
	for _, source := range g.Sources {
		if err := s.projectSource(g, source); err != nil {
			return err
		}
	}
	if g.Identity.SessionKey != "" {
		if err := s.observeSession(g); err != nil {
			return err
		}
	}
	return nil
}

func (s *State) CommitSession(record Record, ref adapters.SessionRef, sess *Session, status SessionPublication) error {
	return s.Atomic(func() error {
		if err := s.RecordArchive(record); err != nil {
			return err
		}
		pending, err := s.projectSession(ref, sess, status)
		if err != nil {
			return err
		}
		if err := s.PutSession(ref, sess); err != nil {
			return err
		}
		if err := s.MarkArchiveApplied(record.Key); err != nil {
			return err
		}
		if pending {
			return s.MarkManifestPending(ref)
		}
		return s.ManifestWritten(ref)
	})
}

func (s *State) ObserveFile(key string, file *File) error {
	return s.Atomic(func() error {
		if err := s.RecordFile(file); err != nil {
			return err
		}
		return s.recordObservation(key, file)
	})
}

func (s *State) ImportPlan(record Record) error {
	return s.Atomic(func() error { return s.RecordArchive(record) })
}

func (s *State) AllArchiveStreams(kind string) ([]string, error) {
	return queryAll(s, "SELECT DISTINCT stream FROM archive_records WHERE kind=? ORDER BY stream", []any{kind}, func(rows *sql.Rows, r *string) error { return rows.Scan(r) })
}

func (s *State) ArchiveApplied(key string) (bool, error) {
	var count int
	_, err := s.queryRow("SELECT COUNT(*) FROM archive_applied WHERE key=?", []any{key}, &count)
	return count == 1, err
}

func (s *State) MarkArchiveApplied(key string) error {
	return s.exec("INSERT OR IGNORE INTO archive_applied VALUES(?)", key)
}

func (s *State) Checkpoint() (string, bool, error) {
	var token string
	var clean bool
	_, err := s.queryRow("SELECT token,clean FROM archive_checkpoint WHERE singleton=1", nil, &token, &clean)
	return token, clean, err
}

func (s *State) SetCheckpoint(token string, clean bool) error {
	return s.Atomic(func() error { return s.exec("INSERT OR REPLACE INTO archive_checkpoint VALUES(1,?,?)", token, clean) })
}

func (s *State) Binding() (*Binding, error) {
	var binding Binding
	found, err := s.queryRow("SELECT schema,destination,store_id,machine_id FROM binding WHERE singleton=1", nil, &binding.Schema, &binding.Destination, &binding.StoreID, &binding.MachineID)
	if !found || err != nil {
		return nil, err
	}
	return &binding, nil
}

func (s *State) MarkArchiveSeen(key, token string) error {
	return s.Atomic(func() error { return s.exec("INSERT OR REPLACE INTO archive_scan VALUES(?,?)", key, token) })
}

func (s *State) MissingArchive(prefix, token string) (string, error) {
	var key string
	_, err := s.queryRow("SELECT key FROM archive_records WHERE substr(key,1,?)=? AND key NOT IN (SELECT key FROM archive_scan WHERE token=?) LIMIT 1", []any{len(prefix), prefix, token}, &key)
	return key, err
}

func (s *State) projectSource(g *raw.Generation, source raw.Source) error {
	f := File{Agent: g.Identity.Agent, Root: g.Identity.Root, Path: source.Path, SourcePath: source.SourcePath, Key: g.Identity.Key, SessionKey: g.Identity.SessionKey, Role: adapters.Role(g.Role), Zstd: source.Encoding == "zstd", Size: g.Size, DiskSize: -1, MtimeNS: -1, SHA256: g.SHA256, LineCount: g.LineCount, PrefixSHA256: g.PrefixSHA256, Head: g.Head}
	if err := s.RecordFile(&f); err != nil {
		return err
	}
	if g.DeletedAt > 0 {
		return s.MarkDeleted(source.Path, g.DeletedAt)
	}
	return nil
}

func (s *State) CacheSession(ref adapters.SessionRef, sess *Session) error {
	return s.Atomic(func() error {
		if err := s.PutSession(ref, sess); err != nil {
			return err
		}
		return s.ManifestWritten(ref)
	})
}

func (s *State) observeSession(g *raw.Generation) error {
	ref := adapters.SessionRef{Agent: g.Identity.Agent, Key: g.Identity.SessionKey}
	sess, err := s.Session(ref)
	if err != nil {
		return err
	}
	if sess == nil {
		sess = &Session{Info: map[string]any{}, FirstSeen: g.ObservedAt}
	}
	sess.FirstSeen = min(sess.FirstSeen, g.ObservedAt)
	sess.Info["source_root"] = g.Identity.Root
	if g.Role == string(adapters.RoleMain) {
		maps.Copy(sess.Info, g.Head)
	}
	if err := s.PutSession(ref, sess); err != nil {
		return err
	}
	return s.MarkManifestPending(ref)
}

// Manifest replay is a projection merge: newer durable launches may exist after its view.
// An older manifest must never erase independently committed launch provenance.
func (s *State) mergeManifestSession(ref adapters.SessionRef, sess *Session) (bool, error) {
	existing, err := s.Session(ref)
	if err != nil {
		return false, err
	}
	if existing == nil {
		return false, nil
	}
	beforeFirst, beforeLaunches, beforeSnapshots := sess.FirstSeen, len(sess.Launches), len(sess.Snapshots)
	sess.FirstSeen = min(sess.FirstSeen, existing.FirstSeen)
	ids := map[string]bool{}
	for i := range sess.Launches {
		ids[sess.Launches[i].Envelope.ID] = true
	}
	for i := range existing.Launches {
		launch := &existing.Launches[i]
		if !ids[launch.Envelope.ID] {
			sess.Launches = append(sess.Launches, *launch)
		}
	}
	slices.SortFunc(sess.Launches, func(a, b Launch) int {
		return cmp.Or(cmp.Compare(a.Envelope.RecordedAt, b.Envelope.RecordedAt), strings.Compare(a.Envelope.ID, b.Envelope.ID))
	})
	snapshots, err := MergeSnapshots(sess.Snapshots, existing.Snapshots)
	if err != nil {
		return false, err
	}
	sess.Snapshots = snapshots
	return beforeFirst != sess.FirstSeen || beforeLaunches != len(sess.Launches) || beforeSnapshots != len(sess.Snapshots), nil
}

// MergeSnapshots preserves distinct observed transitions (including A -> B -> A), while
// ignoring disposable checked-at differences in duplicate views of one observation.
func MergeSnapshots(a, b []SnapshotEntry) ([]SnapshotEntry, error) {
	out := slices.Clone(a)
	seen := map[string]bool{}
	for i := range out {
		key, err := snapshotIdentity(out[i])
		if err != nil {
			return nil, err
		}
		seen[key] = true
	}
	for i := range b {
		key, err := snapshotIdentity(b[i])
		if err != nil {
			return nil, err
		}
		if !seen[key] {
			out = append(out, b[i])
			seen[key] = true
		}
	}
	slices.SortStableFunc(out, func(a, b SnapshotEntry) int {
		return cmp.Or(cmp.Compare(a.TakenAt, b.TakenAt), strings.Compare(a.ID, b.ID), strings.Compare(a.Git.Head, b.Git.Head), strings.Compare(a.Git.Branch, b.Git.Branch))
	})
	return out, nil
}

func snapshotIdentity(entry SnapshotEntry) (string, error) {
	entry.CheckedAt = 0
	body, err := jsonx.SortedCompact(entry)
	return string(body), err
}

// WalkArchive bounds historical metadata memory to one descriptor. The callback may update
// disposable projections; the immutable record cursor itself is never changed during replay.
func (s *State) WalkArchive(kind, stream string, visit func(Record) error) error {
	return s.walkArchive("SELECT key,kind,stream,id,parent,sequence,committed,body FROM archive_records WHERE kind=? AND stream=? ORDER BY sequence,key", []any{kind, stream}, visit)
}

func (s *State) WalkPendingChildren(kind, stream string, sequence uint64, parent string, visit func(Record) error) error {
	return s.walkArchive("SELECT a.key,a.kind,a.stream,a.id,a.parent,a.sequence,a.committed,a.body FROM archive_records AS a WHERE a.kind=? AND a.stream=? AND a.sequence=? AND a.parent=? AND a.committed=0 AND NOT EXISTS(SELECT 1 FROM archive_records AS c WHERE c.kind=a.kind AND c.stream=a.stream AND c.id=a.id AND c.committed=1) ORDER BY a.key", []any{kind, stream, sequence, parent}, visit)
}

func (s *State) walkArchive(query string, args []any, visit func(Record) error) error {
	if err := s.prepare(query); err != nil {
		return err
	}
	rows, err := s.stmts[query].QueryContext(s.ctx, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() // read-only; rows.Err reports cursor failures
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.Key, &record.Kind, &record.Stream, &record.ID, &record.Parent, &record.Sequence, &record.Committed, &record.Body); err != nil {
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return rows.Err()
}

type SessionPublication string

const (
	SessionLaunch          SessionPublication = "launch"
	SessionManifestCurrent SessionPublication = "manifest_current"
	SessionManifestStale   SessionPublication = "manifest_stale"
)

func (s *State) projectSession(ref adapters.SessionRef, sess *Session, status SessionPublication) (bool, error) {
	pending := status != SessionManifestCurrent
	switch status {
	case SessionLaunch:
	case SessionManifestCurrent, SessionManifestStale:
		changed, err := s.mergeManifestSession(ref, sess)
		if err != nil {
			return false, err
		}
		pending = pending || changed
	default:
		return false, errors.New("state session: unknown publication outcome")
	}
	before := maps.Clone(sess.Info)
	if err := s.projectSessionInfo(ref, sess); err != nil {
		return false, err
	}
	return pending || !reflect.DeepEqual(before, sess.Info), nil
}

// projectSessionInfo selects metadata from evidence, never descriptor replay order. Main-file
// observations take precedence; without one, the latest launch carrying a cwd supplies it.
// Launch event order is independent of snapshot capture-time transition order.
func (s *State) projectSessionInfo(ref adapters.SessionRef, sess *Session) error {
	files, err := s.SessionFiles(ref)
	if err != nil {
		return err
	}
	if sess.Info == nil {
		sess.Info = map[string]any{}
	}
	slices.SortFunc(sess.Launches, func(a, b Launch) int {
		return cmp.Or(cmp.Compare(a.Envelope.RecordedAt, b.Envelope.RecordedAt), strings.Compare(a.Envelope.ID, b.Envelope.ID))
	})
	for i := range sess.Launches {
		entry := &sess.Launches[i]
		cwd, _ := entry.Envelope.Payload["cwd"].(string)
		if cwd == "" {
			input, _ := entry.Envelope.Payload["hook_input"].(map[string]any)
			cwd, _ = input["cwd"].(string)
		}
		if cwd != "" {
			sess.Info["cwd"] = cwd
		}
		if entry.Root != "" {
			sess.Info["source_root"] = entry.Root
		}
	}
	MergeFileInfo(sess.Info, files)
	for i := range files {
		if files[i].Role == adapters.RoleMain && files[i].Root != "" {
			sess.Info["source_root"] = files[i].Root
			break
		}
	}
	return nil
}
