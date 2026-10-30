package sessioncapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestVerifiedOptionalRepairRestartAutonomouslyAttachesLatest(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	crash := errors.New("verified crash")
	inject := false
	o.Fault = func(stage string) error {
		if inject && stage == "optional-repair-verified" {
			return crash
		}
		return nil
	}
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	capture(t, q, r)
	enrichOnce(t, q, snapshotOptions("artifact"))
	m, _, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	backend := &optionalBackend{Backend: newBackend(t), key: m.Sources[1].Object.Key, stage: "upload"}
	backend.fail.Store(true)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	backend.fail.Store(false)
	inject = true
	if did, err := q.optionalRepairNext(t.Context(), backend, runtimeDefaults(RuntimeOptions{})); !did || !errors.Is(err, crash) {
		t.Fatalf("verification crash %v %v", did, err)
	}
	var state string
	if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM optional_repairs`).Scan(&state); err != nil || state != "verified_attachment_pending" {
		t.Fatal("lost attachment work", state, err)
	}
	write(t, r.Path, "first\nlatest after verification\n")
	raw := capture(t, q, r)
	var before int
	if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM optional_repairs WHERE last_error='unavailable' AND contract<>'' AND software<>''`).Scan(&before); err != nil || before != 1 {
		t.Fatal("missing retained row-level quarantine evidence", before, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	inject = false
	q, err = Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx, backend, RuntimeOptions{PollInterval: time.Millisecond * 5}) }()
	deadline := time.After(3 * time.Second)
	for {
		remote, readErr := publication.Read(t.Context(), backend, r.Identity)
		if readErr == nil && remote.Sources[0].Object == raw.Sources[0].Object && remote.Sources[1].Withheld == nil {
			if remote.Sources[1].Object != m.Sources[1].Object || remote.Sources[1].CapturedAtNS != m.Sources[1].CapturedAtNS {
				t.Fatal("attachment changed artifact bytes/time")
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("runtime stopped before autonomous attachment: %v", err)
		case <-deadline:
			t.Fatal("verified repair stranded without hook")
		case <-time.After(time.Millisecond * 5):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRowContext(t.Context(), `SELECT state FROM optional_repairs`).Scan(&state); err != nil || state != "repaired" {
		t.Fatal("attachment not completed", state, err)
	}
}

func TestPreparedCleanupFailureVisibleAndOrphansRetainSlots(t *testing.T) {
	dir, o, r := setup(t)
	o.EnableEnrichment = true
	injected := errors.New("cleanup failed")
	o.Fault = func(stage string) error {
		if stage == "prepared-before-cleanup" {
			return injected
		}
		return nil
	}
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	capture(t, q, r)
	enrichOnce(t, q, snapshotOptions("worker body"))
	workBefore, err := q.EnrichmentWorkStatus(t.Context(), "", 10)
	if err != nil || len(workBefore) != 1 || workBefore[0].State != "retry" || workBefore[0].ResultID != "" || workBefore[0].AttachedSequence != 0 {
		t.Fatal("cleanup failure falsely completed worker result", err)
	}
	for n := range 3 {
		// Direct builder must return cleanup errors even after successful snapshot.
		m, _, err := q.latest(t.Context(), r.Identity)
		if err != nil {
			t.Fatal(err)
		}
		spec := EnrichmentSpec{Kind: "snapshot", Identity: r.Identity, BasisPublication: m.PublicationID, BasisSequence: m.Sequence, Source: SourceCoordinate{Path: r.Path}}
		if _, err := q.buildEnrichment(t.Context(), spec, runtimeDefaults(snapshotOptions("body"))); !errors.Is(err, injected) {
			t.Fatalf("cleanup %d hidden: %v", n, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "prepared"))
	if err != nil || len(entries) != 4 {
		t.Fatal("orphans not retained", err)
	}
	if _, _, err := q.preparedSlot(); !errors.Is(err, ErrQuota) {
		t.Fatal("orphan slots not charged", err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, _, err := q.preparedSlot(); !errors.Is(err, ErrQuota) {
		t.Fatal("restart lost orphan charges", err)
	}
	work, err := q.EnrichmentWorkStatus(t.Context(), "", 10)
	if err != nil || len(work) == 0 || work[0].State == "attached" {
		t.Fatal("false completed enrichment", err)
	}
}

func TestOptionalRepairQuotaTerminalReserveKeepsRawPublishing(t *testing.T) {
	for _, kind := range []string{"bytes", "records"} {
		t.Run(kind, func(t *testing.T) {
			q, r, m := optionalFixture(t)
			switch kind {
			case "bytes":
				if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=used WHERE name='enrichment'`); err != nil {
					t.Fatal(err)
				}
			case "records":
				_, err := q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO optional_repairs(id,session,source_hash,epoch,commitment,source,evidence,identity,state,stage,contract,software) SELECT 'full-'||x,'quota','hash','epoch','commitment','{}','{}','{}','quarantined','quota','contract','test' FROM n`, MaxOptionalRepairRecords)
				if err != nil {
					t.Fatal(err)
				}
			}
			backend := newBackend(t)
			for range 3 {
				if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
					t.Fatal("quota became delivery fatal", err)
				}
			}
			write(t, r.Path, "first\nhealthy raw under repair quota\n")
			raw := capture(t, q, r)
			for range 3 {
				if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
					t.Fatal(err)
				}
			}
			remote, err := publication.Read(t.Context(), backend, r.Identity)
			if err != nil || remote.Sources[0].Object != raw.Sources[0].Object || remote.Sources[1].Withheld == nil {
				t.Fatal("repair exhaustion blocked raw", err)
			}
			status, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			visible := false
			for _, s := range status {
				visible = visible || s.OptionalRepairGap == "quota"
				if s.Sequence == m.Sequence && s.State == "published" {
					t.Fatal("blocked composite falsely ACKed")
				}
			}
			if !visible {
				t.Fatal("terminal quota gap invisible")
			}
			// The coordinator must continue running, not fail on bounded admission.
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- q.Run(ctx, backend, RuntimeOptions{PollInterval: time.Millisecond * 5}) }()
			select {
			case err := <-done:
				cancel()
				t.Fatalf("quota stopped Run: %v", err)
			case <-time.After(time.Millisecond * 30):
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := q.ReplayOptionalRepairQuota(t.Context(), r.Identity); !errors.Is(err, ErrQuota) {
				t.Fatal("replay ignored admission quota", err)
			}
			if kind == "bytes" {
				if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=? WHERE name='enrichment'`, MaxEnrichmentMetadataBytes); err != nil {
					t.Fatal(err)
				}
				if err := q.ReplayOptionalRepairQuota(t.Context(), r.Identity); err != nil {
					t.Fatal(err)
				}
				if did, err := q.optionalRepairNext(t.Context(), backend, runtimeDefaults(RuntimeOptions{})); err != nil || !did {
					t.Fatal("replayed quota repair not scheduled", did, err)
				}
				for range 3 {
					if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
						t.Fatal(err)
					}
				}
				repaired, err := publication.Read(t.Context(), backend, r.Identity)
				if err != nil || repaired.Sources[0].Object != raw.Sources[0].Object || repaired.Sources[1].Withheld != nil || repaired.Sources[1].CapturedAtNS != m.Sources[1].CapturedAtNS {
					t.Fatal("quota replay failed retained latest-view repair", err)
				}
				statuses, err := q.Status(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range statuses {
					if entry.OptionalRepairGap != "" {
						t.Fatal("replayed terminal gap not cleared")
					}
				}
			}
		})
	}
}

func TestOpenDropsRetiredHistoryObjectsAndRecomputesBudget(t *testing.T) {
	q, _, _ := optionalFixture(t)
	for _, statement := range []string{
		`CREATE TABLE optional_repair_history(id INTEGER PRIMARY KEY,repair TEXT NOT NULL,at INTEGER NOT NULL,stage TEXT NOT NULL,reason TEXT NOT NULL)`,
		`CREATE INDEX optional_repair_history_owner ON optional_repair_history(repair)`,
		`CREATE TRIGGER optional_repair_history_cap BEFORE INSERT ON optional_repair_history BEGIN SELECT 1; END`,
		`CREATE TABLE enrichment_history (id INTEGER PRIMARY KEY, work TEXT NOT NULL, state TEXT NOT NULL, at INTEGER NOT NULL, reason TEXT NOT NULL)`,
		`INSERT INTO optional_repair_history(repair,at,stage,reason) VALUES('x',1,'s','r')`,
		`INSERT INTO enrichment_history(work,state,at,reason) VALUES('x','s',1,'r')`,
		`UPDATE metadata_budgets SET used=used+100000 WHERE name='enrichment'`,
	} {
		if _, err := q.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.initEnrichment(t.Context()); err != nil {
		t.Fatal(err)
	}
	var retired int
	if err := q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE name LIKE '%\_history%' ESCAPE '\'`).Scan(&retired); err != nil || retired != 0 {
		t.Fatal("retired objects remain", retired, err)
	}
	var used, want int64
	if err := q.db.QueryRowContext(t.Context(), `SELECT used FROM metadata_budgets WHERE name='enrichment'`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRowContext(t.Context(), `SELECT coalesce((SELECT SUM(`+budgetExpression("enrichment_work", "enrichment_work")+`) FROM enrichment_work),0)+coalesce((SELECT SUM(128+length(id)+length(body)) FROM enrichment_results),0)+coalesce((SELECT SUM(`+budgetExpression("optional_repairs", "optional_repairs")+`) FROM optional_repairs),0)`).Scan(&want); err != nil || used != want {
		t.Fatal("budget not recomputed without history", used, want, err)
	}
}
