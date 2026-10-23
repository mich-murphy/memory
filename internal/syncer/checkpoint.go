package syncer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

// checkpoint is legacy v3 machine-wide recovery bookkeeping, not a per-source durable
// capture watermark or proof of reader/importer acceptance. The replacement separates
// these coordinates; see docs/session-capture-contract.md.
type checkpoint struct {
	Schema    int    `json:"schema"`
	MachineID string `json:"machine_id"`
	Token     string `json:"token"`
	Complete  bool   `json:"complete"`
}

func (s *Syncer) checkpointKey() string { return s.machinePrefix() + "checkpoint.json" }
func (s *Syncer) readCheckpoint(ctx context.Context) (checkpoint, error) {
	body, err := s.readRecord(ctx, s.checkpointKey())
	if err != nil {
		return checkpoint{}, err
	}
	var point checkpoint
	if err := jsonx.Decode(body, &point); err != nil {
		return point, err
	}
	if point.Schema != 3 || point.MachineID != s.cfg.MachineID || point.Token == "" {
		return point, errors.New("archive checkpoint: invalid identity")
	}
	return point, nil
}

func (s *Syncer) matchesCheckpoint(ctx context.Context) (bool, error) {
	token, clean, err := s.db.Checkpoint()
	if err != nil {
		return false, err
	}
	point, err := s.readCheckpoint(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !clean || !point.Complete || token != point.Token {
		return false, nil
	}
	records, err := exclusion.Load(s.cfg.StateDir)
	if err != nil {
		return false, err
	}
	s.opts.Exclusions = records
	return true, nil
}

func (s *Syncer) beginCheckpoint(ctx context.Context) error {
	s.runToken = rand.Text()
	if err := s.db.SetCheckpoint(s.runToken, false); err != nil {
		return err
	}
	body, err := jsonx.SortedCompact(checkpoint{Schema: 3, MachineID: s.cfg.MachineID, Token: s.runToken, Complete: false})
	if err != nil {
		return err
	}
	return s.putRecord(ctx, s.checkpointKey(), body)
}

func (s *Syncer) verifyWriter(ctx context.Context) error {
	point, err := s.readCheckpoint(ctx)
	if err != nil {
		return err
	}
	if point.Token != s.runToken || point.Complete {
		return errors.New("archive: another writer changed machine checkpoint")
	}
	return nil
}

func (s *Syncer) sealCheckpoint(ctx context.Context) error {
	if err := s.verifyWriter(ctx); err != nil {
		return err
	}
	body, err := jsonx.SortedCompact(checkpoint{Schema: 3, MachineID: s.cfg.MachineID, Token: s.runToken, Complete: true})
	if err != nil {
		return err
	}
	if err := s.putRecord(ctx, s.checkpointKey(), body); err != nil {
		return err
	}
	return s.db.SetCheckpoint(s.runToken, true)
}

// Reconcile the touched stream independently of cache freshness. A checkpoint is merely
// an acceleration hint; only enumerated immutable parent chains select content.
func (s *Syncer) refreshStream(ctx context.Context, kind, agent, key string) error {
	prefix := s.publicationPrefix(kind, agent, key)
	if err := s.enumerateRecords(ctx, prefix, false); err != nil {
		return err
	}
	if err := s.reconcileStream(ctx, kind, streamID(agent, key)); err != nil {
		return fmt.Errorf("archive stream %s: %w", key, err)
	}
	return nil
}

func (s *Syncer) enumerateRecords(ctx context.Context, prefix string, cold bool) error {
	token := rand.Text()
	keys := make([]string, 0, state.ArchiveSeenBatchSize)
	flush := func() error {
		if err := s.db.MarkArchiveSeenBatch(keys, token); err != nil {
			return err
		}
		keys = keys[:0]
		return nil
	}
	err := s.listPages(ctx, prefix, func(objects []store.Object) error {
		return s.importListedPage(ctx, objects, cold, func(key string) error {
			keys = append(keys, key)
			if len(keys) == state.ArchiveSeenBatchSize {
				return flush()
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	missing, err := s.db.MissingArchive(prefix, token)
	if err != nil {
		return err
	}
	if missing != "" {
		return fmt.Errorf("archive: previously recorded immutable object is missing: %s", missing)
	}
	return nil
}

// importListedPage finishes its bounded metadata read before any remote import or
// marker callback. Cached warm records need no per-object SQLite read.
func (s *Syncer) importListedPage(ctx context.Context, objects []store.Object, cold bool, seen func(string) error) error {
	keys := make([]string, len(objects))
	for i, object := range objects {
		keys[i] = object.Key
	}
	sizes, err := s.db.ArchiveRecordSizes(keys)
	if err != nil {
		return err
	}
	for _, object := range objects {
		size, found := sizes[object.Key]
		if err := s.importListedObjectSize(ctx, object, cold, size, found); err != nil {
			return err
		}
		if err := seen(object.Key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) importListedObject(ctx context.Context, object store.Object, cold bool) error {
	size, found, err := s.db.ArchiveRecordSize(object.Key)
	if err != nil {
		return err
	}
	return s.importListedObjectSize(ctx, object, cold, size, found)
}

func (s *Syncer) importListedObjectSize(ctx context.Context, object store.Object, cold bool, size int64, found bool) error {
	if !found || cold {
		if err := s.importRemote(ctx, object.Key); err != nil {
			return err
		}
	} else if object.Size != size {
		return errors.New("archive: immutable record size changed")
	}

	if cold {
		cached, err := s.db.ArchiveRecord(object.Key)
		if err != nil {
			return err
		}
		if cached.Committed {
			p, err := decodePublication(cached.Body)
			if err != nil {
				return err
			}
			if err := s.verifyPublication(ctx, p); err != nil {
				return err
			}
		}
	}
	return nil
}
