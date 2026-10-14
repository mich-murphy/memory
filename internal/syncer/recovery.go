package syncer

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

type storeIdentity struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
}

func (s *Syncer) destination() (string, error) {
	if s.cfg.LocalStore != "" {
		root, err := filepath.Abs(s.cfg.LocalStore)
		if err != nil {
			return "", err
		}
		resolution, err := capturepolicy.Resolve(root)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if len(resolution.Identities) > 0 {
			root = resolution.Identities[len(resolution.Identities)-1]
		}
		if err == nil {
			root = resolution.Path
		}
		return "local:" + filepath.Clean(root) + ":v3", nil
	}
	u, err := url.Parse(s.cfg.R2Endpoint)
	if err != nil {
		return "", errors.New("archive: invalid endpoint identity")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("archive: endpoint identity must not contain credentials, query or fragment")
	}
	u.Host = strings.ToLower(u.Host)
	u.Scheme = strings.ToLower(u.Scheme)
	u.Path = strings.TrimRight(u.Path, "/")
	return "r2:" + u.String() + "/" + s.cfg.R2Bucket + ":v3", nil
}

func (s *Syncer) bind(ctx context.Context) error {
	if !raw.ValidKey(s.cfg.MachineID) || strings.Contains(s.cfg.MachineID, "/") {
		return errors.New("archive: invalid machine id; requires a UTF-8 key without slashes")
	}
	destination, err := s.destination()
	if err != nil {
		return err
	}
	bound, err := s.db.Binding()
	if err != nil {
		return err
	}
	if bound != nil && (bound.Schema != 3 || bound.Destination != destination || bound.MachineID != s.cfg.MachineID) {
		return errors.New("state binding: incompatible format, destination or machine id; use a separate state directory")
	}
	body, err := s.readRecord(ctx, "v3/store.json")
	if errors.Is(err, store.ErrNotFound) {
		proposed := storeIdentity{Schema: 3, ID: rand.Text()}
		encoded, err := jsonx.SortedCompact(proposed)
		if err != nil {
			return err
		}
		cerr := s.bucket.Create(ctx, "v3/store.json", encoded)
		body, err = s.readRecord(ctx, "v3/store.json")
		if err != nil {
			return errors.Join(cerr, err)
		}
	} else if err != nil {
		return err
	}
	var identity storeIdentity
	if err := jsonx.Decode(body, &identity); err != nil {
		return err
	}
	if identity.Schema != 3 || identity.ID == "" {
		return errors.New("archive: invalid store identity")
	}
	return s.db.Bind(state.Binding{Schema: 3, Destination: destination, StoreID: identity.ID, MachineID: s.cfg.MachineID})
}

func (s *Syncer) list(ctx context.Context, prefix string, visit func(store.Object) error) error {
	token := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := s.bucket.List(ctx, prefix, token, 1000)
		if err != nil {
			return err
		}
		for _, object := range page.Objects {
			if !strings.HasPrefix(object.Key, prefix) || !raw.ValidKey(object.Key) {
				return errors.New("archive list: invalid object key")
			}
			if err := visit(object); err != nil {
				return err
			}
		}
		if page.Next == "" {
			return nil
		}
		if page.Next == token {
			return errors.New("archive list: repeated continuation token")
		}
		token = page.Next
	}
}

func (s *Syncer) restoreExclusions(ctx context.Context) error {
	prefix := s.machinePrefix() + "exclusions/"
	if err := s.list(ctx, prefix, func(object store.Object) error {
		body, err := s.readRecord(ctx, object.Key)
		if err != nil {
			return err
		}
		var record exclusion.Record
		if err := jsonx.Decode(body, &record); err != nil {
			return err
		}
		if object.Key != prefix+raw.SHA256(body)+".json" {
			return errors.New("archive exclusion: record checksum mismatch")
		}
		return exclusion.Save(s.cfg.StateDir, record)
	}); err != nil {
		return err
	}
	return nil
}

func (s *Syncer) recoverExclusions(ctx context.Context) error {
	if err := s.restoreExclusions(ctx); err != nil {
		return err
	}
	prefix := s.machinePrefix() + "exclusions/"
	records, err := exclusion.Load(s.cfg.StateDir)
	if err != nil {
		return err
	}
	s.opts.Exclusions = records
	for _, record := range records {
		body, err := jsonx.SortedCompact(record)
		if err != nil {
			return err
		}
		if err := s.putRecord(ctx, prefix+raw.SHA256(body)+".json", body); err != nil {
			return err
		}
	}
	return nil
}

// Recover completes remote enumeration and validation before any new capture, launch,
// tombstone or export. A failed/partial rebuild is never interpreted as local absence.
func (s *Syncer) Recover(ctx context.Context) error {
	if s.policyErr != nil {
		return s.policyErr
	}
	if s.recovered {
		return nil
	}
	if err := s.bind(ctx); err != nil {
		return err
	}
	warm, err := s.matchesCheckpoint(ctx)
	s.warm = warm
	if err != nil {
		return err
	}
	if !warm {
		if err := s.restoreExclusions(ctx); err != nil {
			return err
		}
		if err := s.enumerateRecords(ctx, s.machinePrefix()+"records/", true); err != nil {
			return err
		}
	}
	if err := s.beginCheckpoint(ctx); err != nil {
		return err
	}
	if err := s.recoverExclusions(ctx); err != nil {
		return err
	}
	if !warm {
		if err := s.reconcileCatalog(ctx); err != nil {
			return err
		}
	}
	s.recovered = true
	return nil
}

func (s *Syncer) importRemote(ctx context.Context, key string) error {
	cached, err := s.db.ArchiveRecord(key)
	if err != nil {
		return err
	}
	if cached != nil {
		object, err := s.bucket.Head(ctx, key)
		if err != nil {
			return err
		}
		if object.Size != int64(len(cached.Body)) {
			return errors.New("archive: immutable record size changed")
		}
		sha := object.Metadata["sha256"]
		if local := object.Metadata["content_sha256"]; local != "" {
			sha = local
		}
		if sha != cached.ID {
			return errors.New("archive: immutable record checksum changed")
		}
		return nil
	}
	body, err := s.readRecord(ctx, key)
	if err != nil {
		return err
	}
	p, err := decodePublication(body)
	if err != nil {
		return err
	}
	if err := p.validate(s.cfg.MachineID); err != nil {
		return err
	}
	canonical, id, err := canonicalPublication(p)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, body) {
		return errors.New("archive record: noncanonical descriptor")
	}
	committed := strings.Contains(key, "/commits/")
	if key != s.publicationKey(p, committed, id) {
		return errors.New("archive record: key checksum or identity mismatch")
	}
	if committed {
		if err := s.verifyPublication(ctx, p); err != nil {
			return fmt.Errorf("archive commit %s: %w", key, err)
		}
	}
	return s.db.ImportPlan(recordFor(key, id, p, body, committed))
}

func (s *Syncer) verifyPublication(ctx context.Context, p *publication) error {
	if err := s.verifyReferences(ctx, p, nil); err != nil {
		return err
	}
	for _, d := range p.Dependencies {
		if err := verifyDependencyMetadata(ctx, s.bucket, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) excludedPublication(p *publication) bool {
	if p.File != nil {
		g := p.File
		return s.opts.Exclusions.Excludes(adapters.SessionFile{Agent: p.Agent, Root: g.Identity.Root, Key: p.Key, SessionKey: g.Identity.SessionKey})
	}
	if p.Launch != nil {
		return s.opts.Exclusions.Excludes(adapters.SessionFile{Agent: p.Agent, Root: p.Launch.Root, SessionKey: p.Launch.SessionKey, Path: p.Launch.Envelope.Path})
	}
	if p.Manifest != nil {
		root, _ := p.Manifest.Info["source_root"].(string)
		return s.opts.Exclusions.Excludes(adapters.SessionFile{Agent: p.Agent, Root: root, SessionKey: p.Manifest.SessionKey})
	}
	return false
}

func (s *Syncer) reconcileStream(ctx context.Context, kind, stream string) error {
	for {
		candidate, err := s.pendingChild(ctx, kind, stream)
		if err != nil {
			return err
		}
		if candidate == nil {
			break
		}
		p, err := decodePublication(candidate.Body)
		if err != nil {
			return err
		}
		key := s.publicationKey(p, true, candidate.ID)
		if err := s.putRecord(ctx, key, candidate.Body); err != nil {
			return err
		}
		if err := s.db.ImportPlan(recordFor(key, candidate.ID, p, candidate.Body, true)); err != nil {
			return err
		}
	}
	return s.replayStream(kind, stream)
}

// pendingChild never promotes obsolete plans or elects one among divergent complete children.
func (s *Syncer) pendingChild(ctx context.Context, kind, stream string) (*state.Record, error) {
	tip, err := s.db.CurrentArchive(kind, stream)
	if err != nil {
		return nil, err
	}
	sequence := uint64(1)
	parent := ""
	if tip != nil {
		sequence = tip.Sequence + 1
		parent = tip.ID
	}
	var candidate *state.Record
	err = s.db.WalkPendingChildren(kind, stream, sequence, parent, func(record state.Record) error {
		complete, err := s.completePlan(ctx, &record)
		if err != nil {
			return err
		}
		if !complete {
			return nil
		}
		if candidate != nil {
			return fmt.Errorf("archive: conflicting complete pending children for %s", stream)
		}
		candidate = &record
		return nil
	})
	return candidate, err
}

func (s *Syncer) completePlan(ctx context.Context, record *state.Record) (bool, error) {
	p, err := decodePublication(record.Body)
	if err != nil {
		return false, err
	}
	if s.excludedPublication(p) {
		return false, nil
	}
	err = s.verifyPublication(ctx, p)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Syncer) replayStream(kind, stream string) error {
	var previous *raw.Generation
	return s.db.WalkArchive(kind, stream, func(record state.Record) error {
		if !record.Committed {
			return nil
		}
		p, err := decodePublication(record.Body)
		if err != nil {
			return err
		}
		applied, err := s.db.ArchiveApplied(record.Key)
		if err != nil {
			return err
		}
		if !applied {
			if err := s.applyPublication(record, p, previous); err != nil {
				return err
			}
		}
		if p.File != nil {
			previous = p.File
		}
		return nil
	})
}

func (s *Syncer) applyPublication(r state.Record, p *publication, previous *raw.Generation) error {
	switch p.Kind {
	case "file":
		return s.db.CommitFile(r, s.rawKey(p.Agent, p.Key), p.File, previous)
	case "root":
		return s.db.Atomic(func() error {
			if err := s.db.RecordArchive(r); err != nil {
				return err
			}
			if err := s.db.AddExtraRoot(p.Root.Agent, p.Root.Path); err != nil {
				return err
			}
			return s.db.MarkArchiveApplied(r.Key)
		})
	case "launch":
		return s.applyLaunch(r, p.Launch)
	case "manifest":
		m := p.Manifest
		sess := &state.Session{Info: m.Info, Snapshots: m.Snapshots, FirstSeen: m.FirstSeen}
		sess.Launches = slices.Clone(m.Launches)
		status, err := s.manifestProjectionStatus(m)
		if err != nil {
			return err
		}
		return s.db.CommitSession(r, adapters.SessionRef{Agent: p.Agent, Key: p.Key}, sess, status)
	}
	return errors.New("archive: unhandled publication kind")
}

// LockDir keeps normal same-machine writers serialized even when their cache directories differ.
func LockDir(cfg *config.Config) (string, error) {
	s := &Syncer{cfg: cfg}
	destination, err := s.destination()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.Home, ".local", "state", "agent-sessions-locks", streamID(destination, cfg.MachineID)), nil
}

func (s *Syncer) reconcileCatalog(ctx context.Context) error {
	for _, kind := range []string{"file", "root", "launch", "manifest"} {
		streams, err := s.db.AllArchiveStreams(kind)
		if err != nil {
			return err
		}
		for _, stream := range streams {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.reconcileStream(ctx, kind, stream); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Syncer) manifestProjectionStatus(m *manifest) (state.SessionPublication, error) {
	files, err := s.db.SessionFiles(adapters.SessionRef{Agent: m.Agent, Key: m.SessionKey})
	if err != nil {
		return "", err
	}
	if len(files) != len(m.Transcripts) {
		return state.SessionManifestStale, nil
	}
	for i := range m.Transcripts {
		tr := &m.Transcripts[i]
		_, record, err := s.current("file", m.Agent, tr.Key)
		if err != nil {
			return "", err
		}
		if record == nil || record.Key != tr.Commit {
			return state.SessionManifestStale, nil
		}
	}
	return state.SessionManifestCurrent, nil
}
