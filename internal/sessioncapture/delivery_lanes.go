package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func hasSelectedOptional(m publication.Manifest) bool {
	for n := range m.Sources {
		s := &m.Sources[n]
		if s.Optional && publication.IsSelected(*s) {
			return true
		}
	}
	return false
}

func (q *Queue) optionalDeliveryHead(ctx context.Context, i publication.Identity) (bool, error) {
	var body []byte
	err := q.db.QueryRowContext(ctx, `SELECT manifest FROM jobs WHERE session=? AND state NOT IN ('published','superseded') ORDER BY sequence LIMIT 1`, i.BaseKey()).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var m publication.Manifest
	if json.Unmarshal(body, &m) != nil {
		return false, ErrIntegrity
	}
	return hasSelectedOptional(m), nil
}

func (q *Queue) deliveryLane(i publication.Identity, optional bool) *sync.Mutex {
	lane := "raw"
	if optional {
		lane = "optional"
	}
	return keyedMutex(&q.deliveries, i.BaseKey()+"/"+lane)
}

// The marker is committed under the mutation lock before any optional network
// operation. New raw captures can detach and supersede this composite durably.
// A process death turns delivering into retry during initialization.
func (q *Queue) beginOptionalDelivery(ctx context.Context, m publication.Manifest, es []evidence) (func() error, error) {
	noop := func() error { return nil }
	if !hasSelectedOptional(m) {
		return noop, nil
	}
	if len(es) != len(m.Sources) {
		return noop, ErrIntegrity
	}
	unlock, err := q.lockSession(ctx, m.Identity)
	if err != nil {
		return noop, err
	}
	defer unlock()
	for n := range m.Sources {
		s := m.Sources[n]
		if !s.Optional || !publication.IsSelected(s) {
			continue
		}
		blocked, err := q.unavailableLatch(ctx, m.Identity, s)
		if err != nil {
			return noop, err
		}
		if blocked {
			continue
		}
		if err := q.persistOptionalRepair(ctx, m.Identity, s, es[n], "delivery"); err != nil {
			return noop, err
		}
		if _, err := q.db.ExecContext(ctx, `UPDATE optional_repairs SET state='delivering' WHERE id=?`, repairID(m.Identity, s)); err != nil {
			return noop, err
		}
	}
	return func() error {
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, err := q.db.ExecContext(save, `UPDATE optional_repairs SET state=CASE WHEN EXISTS(SELECT 1 FROM jobs WHERE session=? AND sequence=? AND state='superseded') THEN 'retry' ELSE 'repaired' END,next_retry=0 WHERE session=? AND state='delivering'`, m.Identity.BaseKey(), m.Sequence, m.Identity.BaseKey())
		return err
	}, nil
}

// detachActiveOptional is metadata-only: no policy callback, hash or network.
func (q *Queue) detachActiveOptional(ctx context.Context, m publication.Manifest, es []evidence) (bool, error) {
	detached := false
	for n := range m.Sources {
		src := &m.Sources[n]
		if !src.Optional || !publication.IsSelected(*src) {
			continue
		}
		var active int
		if err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM optional_repairs WHERE id=? AND state='delivering'`, repairID(m.Identity, *src)).Scan(&active); err != nil {
			return false, err
		}
		if active != 0 {
			es[n].SelectionReason = "unavailable"
			detached = true
		}
	}
	return detached, nil
}
