package syncer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
)

func (p *publication) validateManifest() error {
	m := p.Manifest
	if m == nil || m.Schema != 3 || m.Agent != p.Agent || m.MachineID != p.MachineID || m.SessionKey != p.Key || !validTime(m.FirstSeen) || !validTime(m.SyncedAt) || m.Info == nil {
		return errors.New("archive: invalid manifest payload")
	}
	return m.validateMembers()
}

func (m *manifest) validateMembers() error {
	keys := map[string]bool{}
	for i := range m.Transcripts {
		tr := &m.Transcripts[i]
		if !raw.ValidKey(tr.Key) || keys[tr.Key] || !raw.ValidHash(tr.SHA256) || tr.Size < 0 || tr.Object != tr.Commit || len(tr.Pieces) == 0 {
			return errors.New("archive: invalid transcript reference")
		}
		keys[tr.Key] = true
	}
	if err := m.validateLaunchSnapshots(); err != nil {
		return err
	}
	for _, deleted := range m.DeletedLocally {
		if !keys[deleted.Key] || !validTime(deleted.At) || deleted.At <= 0 {
			return errors.New("archive: invalid deletion reference")
		}
	}
	return nil
}

func (m *manifest) validateLaunchSnapshots() error {
	for i := range m.Snapshots {
		if err := validateSnapshot(m.Snapshots[i]); err != nil {
			return err
		}
	}
	for i := range m.Launches {
		if err := validateLaunch(&m.Launches[i]); err != nil {
			return err
		}
		if m.Launches[i].SessionKey != m.SessionKey {
			return errors.New("archive: launch session mismatch")
		}
	}
	if (m.Launch == nil) != (len(m.Launches) == 0) {
		return errors.New("archive: inconsistent first launch")
	}
	if m.Launch != nil {
		a, err := jsonx.SortedCompact(m.Launch)
		if err != nil {
			return err
		}
		b, err := jsonx.SortedCompact(&m.Launches[0])
		if err != nil {
			return err
		}
		if !bytes.Equal(a, b) {
			return errors.New("archive: first launch differs from history")
		}
	}
	return nil
}

func validateLaunch(record *launchRecord) error {
	if record == nil || !raw.ValidKey(record.SessionKey) || !raw.ValidPath(record.Root) {
		return errors.New("archive: invalid launch identity")
	}
	if err := record.Envelope.Validate(); err != nil {
		return err
	}
	if record.Snapshot.ID != "" {
		return validateSnapshot(record.Snapshot)
	}
	if record.Snapshot.MemoryID != nil {
		return errors.New("archive: memory snapshot without harness")
	}
	return nil
}

func validateSnapshot(snap state.SnapshotEntry) error {
	if !raw.ValidHash(snap.ID) || snap.MemoryID != nil && !raw.ValidHash(*snap.MemoryID) || !validTime(snap.TakenAt) || !validTime(snap.CheckedAt) {
		return errors.New("archive: invalid snapshot reference")
	}
	switch snap.Timing {
	case "captured", "in_session", "post_hoc":
	default:
		return errors.New("archive: unsupported snapshot timing")
	}
	for _, mtime := range snap.Mtimes {
		if !validTime(mtime) {
			return errors.New("archive: invalid snapshot mtime")
		}
	}
	return nil
}

func dependencyMap(deps []dependency) (map[string]dependency, error) {
	byKey := make(map[string]dependency, len(deps))
	for _, dep := range deps {
		if _, exists := byKey[dep.Object]; exists {
			return nil, errors.New("archive: duplicate dependency identity")
		}
		byKey[dep.Object] = dep
	}
	return byKey, nil
}

// verifyReferences validates the typed reference graph, not merely arbitrary dependencies
// supplied by a descriptor. A forged manifest cannot omit a file/set/blob dependency.
func (s *Syncer) verifyReferences(ctx context.Context, p *publication, bodies map[string][]byte) error {
	if p.File != nil {
		return nil
	}
	deps, err := dependencyMap(p.Dependencies)
	if err != nil {
		return err
	}
	snapshots, err := s.verifySessionReferences(ctx, p, deps)
	if err != nil {
		return err
	}
	return s.verifySnapshotReferences(ctx, p.Agent, snapshots, bodies, deps)
}

func (s *Syncer) verifySessionReferences(ctx context.Context, p *publication, deps map[string]dependency) ([]state.SnapshotEntry, error) {
	var snapshots []state.SnapshotEntry
	if p.Launch != nil {
		if p.Launch.Snapshot.ID != "" {
			snapshots = append(snapshots, p.Launch.Snapshot)
		}
	}
	if p.Manifest != nil {
		m := p.Manifest
		for i := range m.Transcripts {
			if err := s.verifyTranscriptReference(ctx, p, &m.Transcripts[i], deps); err != nil {
				return nil, err
			}
		}
		for i := range m.Launches {
			if err := s.verifyLaunchReference(ctx, p, &m.Launches[i], deps); err != nil {
				return nil, err
			}
		}
		snapshots = m.Snapshots
	}
	return snapshots, nil
}

func (s *Syncer) verifySnapshotReferences(ctx context.Context, agent string, snapshots []state.SnapshotEntry, bodies map[string][]byte, deps map[string]dependency) error {
	for i := range snapshots {
		for _, id := range snapshotIDs(snapshots[i]) {
			expected, err := s.snapshotDependencies(ctx, id, bodies, agent)
			if err != nil {
				return err
			}
			for _, dep := range expected {
				if got, ok := deps[dep.Object]; !ok || got != dep {
					return fmt.Errorf("archive: missing or inconsistent snapshot dependency %s", dep.Object)
				}
			}
		}
	}
	return nil
}

func (s *Syncer) referencedPublication(ctx context.Context, key string, deps map[string]dependency) (*publication, error) {
	dep, ok := deps[key]
	if !ok || dep.Encoding != "plain" || !strings.Contains(key, "/commits/") {
		return nil, errors.New("archive: missing committed metadata dependency")
	}
	var body []byte
	cached, err := s.db.ArchiveRecord(key)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		body = cached.Body
	} else {
		body, err = s.readRecord(ctx, key)
		if err != nil {
			return nil, err
		}
	}
	if raw.SHA256(body) != dep.SHA256 || int64(len(body)) != dep.Size {
		return nil, errors.New("archive: metadata dependency checksum mismatch")
	}
	p, err := decodePublication(body)
	if err != nil {
		return nil, err
	}
	if err := p.validate(s.cfg.MachineID); err != nil {
		return nil, err
	}
	if key != s.publicationKey(p, true, dep.SHA256) {
		return nil, errors.New("archive: referenced publication identity mismatch")
	}
	return p, nil
}

func (s *Syncer) verifyTranscriptReference(ctx context.Context, parent *publication, tr *transcript, deps map[string]dependency) error {
	file, err := s.referencedPublication(ctx, tr.Commit, deps)
	if err != nil {
		return err
	}
	if file.File == nil || file.Agent != parent.Agent || file.Key != tr.Key || file.File.Identity.SessionKey != parent.Key {
		return errors.New("archive: transcript identity mismatch")
	}
	g := file.File
	if tr.Size != g.Size || tr.SHA256 != g.SHA256 || tr.LineCount != g.LineCount || string(tr.Role) != g.Role || !slices.Equal(tr.Pieces, g.Pieces) || tr.History != s.publicationPrefix("file", parent.Agent, tr.Key)+"commits/" {
		return errors.New("archive: transcript content differs from committed generation")
	}
	return nil
}

func (s *Syncer) verifyLaunchReference(ctx context.Context, parent *publication, record *launchRecord, deps map[string]dependency) error {
	found := false
	for _, dep := range deps {
		if !strings.HasPrefix(dep.Object, s.publicationPrefix("launch", parent.Agent, record.Envelope.ID)+"commits/") {
			continue
		}
		p, err := s.referencedPublication(ctx, dep.Object, deps)
		if err != nil {
			return err
		}
		if p.Launch == nil {
			return errors.New("archive: wrong launch dependency kind")
		}
		expected, err := jsonx.SortedCompact(p.Launch)
		if err != nil {
			return err
		}
		actual, err := jsonx.SortedCompact(record)
		if err != nil {
			return err
		}
		if !bytes.Equal(expected, actual) {
			return errors.New("archive: launch differs from committed record")
		}
		found = true
	}
	if !found {
		return errors.New("archive: launch has no committed dependency")
	}
	return nil
}

// validateFileTransition checks the part of append/fold proofs available without reading
// bodies. Appends inherit the parent's exact immutable prefix representation. Restore
// additionally verifies every decoded byte and the declared whole-file digest.
func validateFileTransition(p *publication, previous *raw.Generation) error {
	g := p.File
	if g == nil || g.Kind != "append" && g.Kind != "fold" {
		return nil
	}
	if previous == nil || previous.DeletedAt != 0 || g.Parent == "" || g.Sequence != previous.Sequence+1 {
		return errors.New("archive: append or fold has no live parent")
	}
	switch g.Kind {
	case "append":
		if g.Role == string(adapters.RoleDB) || g.Size <= previous.Size || g.SHA256 == previous.SHA256 || len(g.Pieces) != len(previous.Pieces)+1 || !slices.Equal(g.Pieces[:len(previous.Pieces)], previous.Pieces) {
			return errors.New("archive: append does not extend parent pieces")
		}
	case "fold":
		if g.Size != previous.Size || g.SHA256 != previous.SHA256 {
			return errors.New("archive: fold changes parent content")
		}
		// Existing v3 explicitly serialized file dependencies and folded the entire file.
		// Keep those historical records readable; the new format retains the first base.
		if p.Dependencies == nil && (!foldable(previous) || len(g.Pieces) != 2 || g.Pieces[0] != previous.Pieces[0]) {
			return errors.New("archive: fold does not retain append base")
		}
	}
	return nil
}
