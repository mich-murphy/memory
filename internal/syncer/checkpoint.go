package syncer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/store"
)

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
	retired := s.machinePrefix() + "records/manifest/"
	err := s.list(ctx, prefix, func(object store.Object) error {
		// Schema 3 manifests, superseded by records/session/ and safe to delete.
		if strings.HasPrefix(object.Key, retired) {
			return nil
		}
		if err := s.importListedObject(ctx, object, cold); err != nil {
			return err
		}
		return s.db.MarkArchiveSeen(object.Key, token)
	})
	if err != nil {
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

func (s *Syncer) importListedObject(ctx context.Context, object store.Object, cold bool) error {
	cached, err := s.db.ArchiveRecord(object.Key)
	if err != nil {
		return err
	}
	if cached == nil || cold {
		if err := s.importRemote(ctx, object.Key); err != nil {
			return err
		}
	} else if object.Size != int64(len(cached.Body)) {
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
