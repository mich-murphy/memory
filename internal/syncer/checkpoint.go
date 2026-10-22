package syncer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/state"
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
	var batch []store.Object
	flush := func() error {
		err := s.importColdBatch(ctx, batch, token)
		batch = batch[:0]
		return err
	}
	err := s.list(ctx, prefix, func(object store.Object) error {
		if !cold {
			if err := s.importListedObject(ctx, object, false); err != nil {
				return err
			}
			return s.db.MarkArchiveSeen(object.Key, token)
		}
		batch = append(batch, object)
		if len(batch) < coldBatchSize {
			return nil
		}
		return flush()
	})
	if err == nil && len(batch) > 0 {
		err = flush()
	}
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

const coldBatchSize = 256

type coldRecord struct {
	object store.Object
	cached *state.Record
	commit *publication
}

// State is not goroutine-safe: only the store checks of cached records may run concurrently.
func (s *Syncer) importColdBatch(ctx context.Context, objects []store.Object, token string) error {
	records := make([]coldRecord, 0, len(objects))
	var prepared error
	for _, object := range objects {
		r, err := s.prepareColdRecord(ctx, object)
		if err != nil {
			prepared = err
			break
		}
		records = append(records, r)
	}
	errs := s.verifyColdRecords(ctx, records)
	for i, r := range records {
		if errs[i] != nil {
			return errs[i]
		}
		if r.cached == nil {
			if err := s.importListedObject(ctx, r.object, true); err != nil {
				return err
			}
		}
		if err := s.db.MarkArchiveSeen(r.object.Key, token); err != nil {
			return err
		}
	}
	if prepared != nil {
		return prepared
	}
	return ctx.Err()
}

func (s *Syncer) prepareColdRecord(ctx context.Context, object store.Object) (coldRecord, error) {
	cached, err := s.db.ArchiveRecord(object.Key)
	if err != nil || cached == nil || !cached.Committed {
		return coldRecord{object: object, cached: cached}, err
	}
	p, err := decodePublication(cached.Body)
	if err != nil {
		return coldRecord{}, err
	}
	if err := s.verifyReferences(ctx, p, nil); err != nil {
		return coldRecord{}, err
	}
	return coldRecord{object: object, cached: cached, commit: p}, nil
}

func (s *Syncer) verifyColdRecords(ctx context.Context, records []coldRecord) []error {
	errs := make([]error, len(records))
	var next atomic.Uint64
	var failed atomic.Bool
	var workers sync.WaitGroup
	for range min(dependencyWorkers, len(records)) {
		workers.Go(func() {
			for !failed.Load() {
				i := int(next.Add(1) - 1)
				if i >= len(records) {
					return
				}
				if errs[i] = s.verifyColdRecord(ctx, records[i]); errs[i] != nil {
					failed.Store(true)
				}
			}
		})
	}
	workers.Wait()
	return errs
}

func (s *Syncer) verifyColdRecord(ctx context.Context, r coldRecord) error {
	if err := ctx.Err(); err != nil || r.cached == nil {
		return err
	}
	release, err := s.dependencySlot(ctx)
	if err != nil {
		return err
	}
	err = s.checkCachedRecord(ctx, r.object.Key, r.cached)
	release()
	if err != nil || r.commit == nil {
		return err
	}
	return s.ensureDependencies(ctx, r.commit.dependencies(), nil)
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
