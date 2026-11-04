package sessioncapture

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// The optional/repair model is separate from TestModelHistories so that test's
// generated sequences stay byte-identical. It drives one session per history with
// a held optional artifact (enrichment attachment), optional upload faults,
// terminal metadata quota, repair, repair replay, restart and policy revocation,
// and checks the selected publication, the spool and the CAS count at every step.

const (
	optionalHistories = 12
	optionalSeed      = 20261104
)

type optOp struct {
	Kind string
	Seed uint64
	Size int
}

type optHistory struct {
	Seed uint64
	Body []byte
	Ops  []optOp
}

// Each spine forces one scenario class; a short random tail follows.
var optSpines = [][]string{
	{"Append", "Capture", "Enrich", "FaultOn", "Deliver", "Repair", "FaultOff", "Repair", "Deliver"},
	{"Append", "Capture", "Enrich", "Quota", "FaultOn", "Deliver", "FaultOff", "ReplayQuota", "Repair", "Deliver"},
	{"Append", "Capture", "Enrich", "FaultOn", "Deliver", "FaultOff", "Repair", "Deliver", "ReplayRepair", "Repair", "Deliver"},
	{"Append", "Capture", "Enrich", "FaultOn", "Deliver", "Restart", "FaultOff", "Repair", "Deliver"},
	{"Append", "Capture", "Enrich", "Deliver", "Revoke", "Deliver", "Append", "Capture", "Deliver", "Unrevoke", "Deliver"},
	{"Append", "Capture", "Enrich", "Deliver", "Append", "Capture", "Enrich", "Deliver"},
}

var optTail = []string{"Append", "Capture", "Enrich", "Deliver", "Deliver", "Repair", "Restart", "FaultOn", "FaultOff", "ReplayRepair", "ReplayQuota", "Quota", "Revoke", "Unrevoke"}

func optBytes(seed uint64, size int) []byte {
	rnd := rand.New(rand.NewPCG(seed, seed^0x5a5a5a5a))
	out := make([]byte, size)
	for i := range out {
		out[i] = 'a' + byte(rnd.IntN(26))
		if i%61 == 60 {
			out[i] = '\n'
		}
	}
	out[size-1] = '\n'
	return out
}

func generateOptionalHistory(i int, seed uint64) optHistory {
	rnd := rand.New(rand.NewPCG(seed, seed^0x0b0b0b0b))
	// Artifact sizes follow the measured shape: small median, heavy tail, capped for CPU.
	size := max(1, min(int(lognormal(rnd, 2048, 1.5)), 1<<16))
	h := optHistory{Seed: seed, Body: optBytes(seed^7, size)}
	kinds := slices.Clone(optSpines[i%len(optSpines)])
	for range 4 {
		kinds = append(kinds, optTail[rnd.IntN(len(optTail))])
	}
	for _, k := range kinds {
		h.Ops = append(h.Ops, optOp{Kind: k, Seed: rnd.Uint64(), Size: 64 + rnd.IntN(2048)})
	}
	return h
}

// optBackend fails uploads of the artifact object while the fault is on.
type optBackend struct {
	*modelBackend
	fmu    sync.Mutex
	fail   bool
	keys   map[string]bool
	failed int
}

func (b *optBackend) PutFile(ctx context.Context, key, filename string) error {
	b.fmu.Lock()
	hit := b.fail && b.keys[key]
	if hit {
		b.failed++
	}
	b.fmu.Unlock()
	if hit {
		return errors.New("model: optional upload fault")
	}
	return b.modelBackend.PutFile(ctx, key, filename)
}

func (b *optBackend) setFail(on bool) {
	b.fmu.Lock()
	b.fail = on
	b.fmu.Unlock()
}

type optStats struct {
	Histories, Ops, Exports                        int
	Attached, Restarts, UploadFaults               int
	WithheldUnavailable, WithheldPolicy            int
	QuotaGaps, QuotaReplays, RepairsRun            int
	RepairReplays, Repaired, Revocations, Restores int
	RawNeverBlocked, SpoolChecks, HeldPayloads     int
	CASBoundChecks                                 int
}

type optRun struct {
	ctx          context.Context
	h            optHistory
	dir          string
	be           *optBackend
	q            *Queue
	opts         Options
	stats        *optStats
	nowSec       atomic.Int64
	revoked      atomic.Bool
	file         []byte
	captured     []byte
	attached     bool
	quotaFull    bool
	everWithheld bool
	exports      int
}

func (r *optRun) ident() publication.Identity {
	return publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: "sess0"}
}

func (r *optRun) path() string { return filepath.Join(r.dir, "src", "r0.jsonl") }

func (r *optRun) req() Request {
	return Request{Identity: r.ident(), SourceID: "src0", Role: "transcript", Path: r.path(), ObservedAtNS: 1}
}

func (r *optRun) openQueue() error {
	q, err := Open(r.ctx, filepath.Join(r.dir, "queue"), r.opts)
	if err != nil {
		return err
	}
	r.q = q
	return nil
}

func runOptionalHistory(ctx context.Context, h optHistory, base string, stats *optStats) (string, string) {
	dir, err := os.MkdirTemp(base, "o-")
	if err != nil {
		return "HARNESS", err.Error()
	}
	defer func() { _ = os.RemoveAll(dir) }()
	r := &optRun{ctx: ctx, h: h, dir: dir, stats: stats}
	r.nowSec.Store(1_700_000_000)
	return r.run()
}

func (r *optRun) run() (id, detail string) {
	r.be = &optBackend{modelBackend: newModelBackend(filepath.Join(r.dir, "remote")), keys: map[string]bool{}}
	defer func() {
		if r.q != nil {
			_ = r.q.Close()
		}
		r.be.fmu.Lock()
		r.stats.UploadFaults += r.be.failed
		r.be.fmu.Unlock()
	}()
	if err := publication.WriteArchive(r.ctx, r.be.inner, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
		return "HARNESS", err.Error()
	}
	r.opts = Options{
		ArchiveID: "archive", MaxSpoolBytes: 1 << 30, EnableEnrichment: true,
		Now: func() time.Time { return time.Unix(r.nowSec.Load(), 0) },
		CheckPolicy: func(_ context.Context, req Request) error {
			if req.Optional && r.revoked.Load() {
				return ErrPolicy
			}
			return nil
		},
	}
	if err := os.MkdirAll(filepath.Dir(r.path()), 0o700); err != nil {
		return "HARNESS", err.Error()
	}
	if err := r.openQueue(); err != nil {
		return "HARNESS", "open: " + err.Error()
	}
	r.stats.Histories++
	for i, op := range r.h.Ops {
		r.stats.Ops++
		if id, d := r.step(op); id != "" {
			return id, fmt.Sprintf("op %d %+v: %s", i, op, d)
		}
		if id, d := r.checkStep(); id != "" {
			return id, fmt.Sprintf("after op %d %+v: %s", i, op, d)
		}
	}
	return r.drain()
}

func (r *optRun) step(op optOp) (string, string) {
	switch op.Kind {
	case "Append":
		return r.appendRaw(op)
	case "Capture":
		return r.captureRaw()
	case "Enrich":
		return r.enrich()
	case "FaultOn", "FaultOff":
		r.be.setFail(op.Kind == "FaultOn")
	case "Quota":
		return r.exhaustQuota()
	case "ReplayQuota":
		return r.replayQuota()
	case "Repair":
		return r.repair()
	case "ReplayRepair":
		return r.replayRepair()
	case "Restart":
		return r.restart()
	case "Revoke":
		return r.revoke()
	case "Unrevoke":
		return r.unrevoke()
	case "Deliver":
		return r.deliver()
	}
	return "", ""
}

func (r *optRun) appendRaw(op optOp) (string, string) {
	data := optBytes(op.Seed, op.Size)
	f, err := os.OpenFile(r.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.Write(data)
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		return "HARNESS", err.Error()
	}
	r.file = append(r.file, data...)
	return "", ""
}

func (r *optRun) captureRaw() (string, string) {
	if len(r.file) == 0 {
		return "", ""
	}
	if _, err := r.q.Capture(r.ctx, r.req()); err != nil {
		return "CAP", "raw capture refused by an optional failure: " + err.Error()
	}
	r.captured = slices.Clone(r.file)
	return "", ""
}

// enrich runs queued enrichment work to completion and notes the attached artifact.
func (r *optRun) enrich() (string, string) {
	if r.captured == nil || r.revoked.Load() || r.quotaFull {
		return "", ""
	}
	opts := snapshotOptions(string(r.h.Body))
	for range 3 {
		did, err := r.q.enrichmentNext(r.ctx, opts)
		if err != nil {
			return "ENRICH", err.Error()
		}
		if !did {
			break
		}
	}
	m, _, err := r.q.latest(r.ctx, r.ident())
	if err != nil {
		return "HARNESS", err.Error()
	}
	for i := range m.Sources {
		if m.Sources[i].Role != "artifact" {
			continue
		}
		if !r.attached {
			r.stats.Attached++
		}
		r.attached = true
		if m.Sources[i].Withheld == nil {
			r.be.fmu.Lock()
			r.be.keys[m.Sources[i].Object.Key] = true
			r.be.fmu.Unlock()
		}
	}
	return "", ""
}

func (r *optRun) exhaustQuota() (string, string) {
	if _, err := r.q.db.ExecContext(r.ctx, `UPDATE metadata_budgets SET limit_bytes=used WHERE name='enrichment'`); err != nil {
		return "HARNESS", err.Error()
	}
	r.quotaFull = true
	return "", ""
}

func (r *optRun) quotaGap() (int, error) {
	var n int
	err := r.q.db.QueryRowContext(r.ctx, `SELECT count(*) FROM jobs WHERE optional_repair_gap='quota'`).Scan(&n)
	return n, err
}

// replayQuota restores the budget, then replays the retained coordinates if the
// terminal gap was set.
func (r *optRun) replayQuota() (string, string) {
	if _, err := r.q.db.ExecContext(r.ctx, `UPDATE metadata_budgets SET limit_bytes=? WHERE name='enrichment'`, MaxEnrichmentMetadataBytes); err != nil {
		return "HARNESS", err.Error()
	}
	r.quotaFull = false
	gap, err := r.quotaGap()
	if err != nil {
		return "HARNESS", err.Error()
	}
	if gap == 0 {
		return "", ""
	}
	if err := r.q.ReplayOptionalRepairQuota(r.ctx, r.ident()); err != nil {
		return "REPLAYQUOTA", err.Error()
	}
	r.stats.QuotaReplays++
	return "", ""
}

func (r *optRun) repair() (string, string) {
	for range 3 {
		r.nowSec.Add(600)
		did, err := r.q.optionalRepairNext(r.ctx, r.be, runtimeDefaults(RuntimeOptions{}))
		if err != nil {
			return "REPAIR", err.Error()
		}
		if !did {
			break
		}
		r.stats.RepairsRun++
	}
	return "", ""
}

func (r *optRun) replayRepair() (string, string) {
	var id string
	err := r.q.db.QueryRowContext(r.ctx, `SELECT id FROM optional_repairs ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ""
	}
	if err != nil {
		return "HARNESS", err.Error()
	}
	if err := r.q.ReplayOptionalRepair(r.ctx, id); err != nil {
		return "REPLAY", err.Error()
	}
	r.stats.RepairReplays++
	return "", ""
}

func (r *optRun) restart() (string, string) {
	if err := r.q.Close(); err != nil {
		return "HARNESS", "close: " + err.Error()
	}
	r.q = nil
	if err := r.openQueue(); err != nil {
		return "RESTART", "reopen: " + err.Error()
	}
	r.stats.Restarts++
	return "", ""
}

func (r *optRun) revoke() (string, string) {
	if !r.attached {
		return "", ""
	}
	r.revoked.Store(true)
	r.stats.Revocations++
	if _, err := r.q.RefreshOptionalSelection(r.ctx, r.ident()); err != nil {
		return "REVOKE", err.Error()
	}
	return "", ""
}

// unrevoke restores policy: the withheld artifact must be selectable again from
// the retained original bytes, which only holds if its payload was never reclaimed.
func (r *optRun) unrevoke() (string, string) {
	if !r.revoked.Load() {
		return "", ""
	}
	r.revoked.Store(false)
	r.stats.Restores++
	if _, err := r.q.RefreshOptionalSelection(r.ctx, r.ident()); err != nil {
		return "UNREVOKE", err.Error()
	}
	return "", ""
}

func (r *optRun) deliver() (string, string) {
	if r.captured == nil {
		return "", ""
	}
	for range 4 {
		if err := r.q.Deliver(r.ctx, r.be, r.ident()); err != nil {
			return "BLOCKED", "optional failure blocked raw delivery: " + err.Error()
		}
	}
	r.stats.RawNeverBlocked++
	_, id, d := r.checkRemote()
	return id, d
}

type optView struct {
	raw       publication.Source
	selected  int
	withheld  int
	reasons   []string
	partial   bool
	rawEpochs []publication.Source
}

func exportBody(exp publication.Export, dest string, s publication.Source) ([]byte, error) {
	idx := slices.IndexFunc(exp.Bodies, func(b publication.Body) bool { return b.EpochID == s.EpochID && b.SourceID == s.SourceID })
	if idx < 0 {
		return nil, errors.New("no exported body")
	}
	return os.ReadFile(filepath.Join(dest, exp.Bodies[idx].Path))
}

// checkRemote exports the selected publication and checks: raw bytes equal the
// last capture; a selected artifact has exactly the expected bytes and exists
// only while attached and not revoked; any withheld artifact is reported by
// Coverage.Enrichment=="partial" plus the withheld marker; a terminal quota gap
// withholds the artifact as unavailable.
func (r *optRun) checkRemote() (optView, string, string) {
	var v optView
	r.exports++
	r.stats.Exports++
	dest := filepath.Join(r.dir, fmt.Sprintf("exp%d", r.exports))
	defer func() { _ = os.RemoveAll(dest) }()
	exp, err := publication.ExportSession(r.ctx, r.be.inner, r.ident(), dest)
	if err != nil {
		return v, "I1", "export: " + err.Error()
	}
	m := exp.Manifest
	v.partial = m.Coverage.Enrichment == "partial" && slices.Contains(m.Coverage.Missing, publication.MissingSourceWithheld)
	selected := 0
	for i := range m.Sources {
		s := m.Sources[i]
		if publication.IsSelected(s) {
			selected++
		}
		if id, d := r.checkSource(exp, dest, s, &v); id != "" {
			return v, id, d
		}
	}
	if len(exp.Bodies) != selected {
		return v, "I1", fmt.Sprintf("%d exported bodies for %d selected sources", len(exp.Bodies), selected)
	}
	if v.withheld > 0 && !v.partial {
		return v, "PARTIAL", fmt.Sprintf("withheld artifact without partial coverage (enrichment=%q missing=%v)", m.Coverage.Enrichment, m.Coverage.Missing)
	}
	if v.selected+v.withheld > 0 && !r.attached {
		return v, "I1", "artifact published before any attachment"
	}
	gap, err := r.quotaGap()
	if err != nil {
		return v, "HARNESS", err.Error()
	}
	if gap > 0 {
		r.stats.QuotaGaps++
		if v.withheld == 0 || v.reasons[0] != "unavailable" {
			return v, "QUOTAGAP", fmt.Sprintf("terminal quota gap but artifact withheld=%d reasons=%v", v.withheld, v.reasons)
		}
	}
	r.noteView(v)
	return v, "", ""
}

func (r *optRun) checkSource(exp publication.Export, dest string, s publication.Source, v *optView) (string, string) {
	switch {
	case s.Role != "artifact":
		body, err := exportBody(exp, dest, s)
		if err != nil || !bytes.Equal(body, r.captured) || s.Optional || s.Withheld != nil {
			return "I1", fmt.Sprintf("raw source (%d bytes, err %v) != last capture (%d bytes)", len(body), err, len(r.captured))
		}
		v.raw = s
		v.rawEpochs = append(v.rawEpochs, s)
	case s.Withheld != nil:
		v.withheld++
		v.reasons = append(v.reasons, s.Withheld.Reason)
	default:
		v.selected++
		body, err := exportBody(exp, dest, s)
		if err != nil || !bytes.Equal(body, r.h.Body) {
			return "ARTIFACT", fmt.Sprintf("selected artifact (%d bytes, err %v) != expected (%d bytes)", len(body), err, len(r.h.Body))
		}
		if r.revoked.Load() {
			return "REVOKED", "revoked artifact still selected"
		}
	}
	return "", ""
}

func (r *optRun) noteView(v optView) {
	if v.withheld > 0 {
		r.everWithheld = true
		switch v.reasons[0] {
		case "unavailable":
			r.stats.WithheldUnavailable++
		case "policy":
			r.stats.WithheldPolicy++
		}
	}
	if v.selected > 0 && r.everWithheld {
		r.stats.Repaired++
	}
}

func (r *optRun) checkStep() (string, string) {
	r.be.mu.Lock()
	violation := r.be.violation
	r.be.mu.Unlock()
	if violation != "" {
		return "I3", violation
	}
	return r.checkSpool(false)
}

func (r *optRun) heldQuery(query string, held map[string]bool) error {
	rows, err := r.q.db.QueryContext(r.ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sha sql.NullString
		if err := rows.Scan(&sha); err != nil {
			return err
		}
		if sha.Valid && sha.String != "" {
			held[sha.String+".zst"] = true
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

// heldFiles are payloads held outside any job: enrichment results and optional
// repairs that are not yet repaired. They are computed from the tables, not from
// payload_refs, so a missing hold trigger cannot hide itself.
func (r *optRun) heldFiles() (map[string]bool, error) {
	held := map[string]bool{}
	for _, query := range []string{
		`SELECT json_extract(CAST(source AS TEXT),'$.object.encoded_sha256') FROM optional_repairs WHERE state!='repaired'`,
		`SELECT json_extract(CAST(body AS TEXT),'$.Object.encoded_sha256') FROM enrichment_results`,
	} {
		if err := r.heldQuery(query, held); err != nil {
			return nil, err
		}
	}
	return held, nil
}

// checkSpool: roots and held payloads exist at every step; at the final drain
// (no crash faults) the spool equals roots plus held.
func (r *optRun) checkSpool(final bool) (string, string) {
	roots, err := (&modelRun{ctx: r.ctx, q: r.q}).rootFiles()
	if err != nil {
		return "HARNESS", err.Error()
	}
	held, err := r.heldFiles()
	if err != nil {
		return "HARNESS", err.Error()
	}
	entries, err := os.ReadDir(filepath.Join(r.q.dir, "payloads"))
	if err != nil {
		return "HARNESS", err.Error()
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}
	r.stats.SpoolChecks++
	r.stats.HeldPayloads += len(held)
	for n := range roots {
		if !have[n] {
			return "RECLAIM", "retained root payload missing from spool: " + n
		}
	}
	for n := range held {
		if !have[n] {
			return "HELD", "held optional payload missing from spool: " + n
		}
	}
	if !final {
		return "", ""
	}
	for n := range have {
		if !roots[n] && !held[n] {
			return "LEAK", "spool payload neither root nor held: " + n
		}
	}
	return "", ""
}

func (r *optRun) casBound() (string, string) {
	var jobs int
	if err := r.q.db.QueryRowContext(r.ctx, `SELECT count(*) FROM jobs`).Scan(&jobs); err != nil {
		return "HARNESS", err.Error()
	}
	r.be.mu.Lock()
	cas := len(r.be.cas)
	r.be.mu.Unlock()
	r.stats.CASBoundChecks++
	// Superseded selection views are jobs that never publish, so CAS writes never exceed jobs.
	if cas > jobs {
		return "COUNTS", fmt.Sprintf("%d CAS writes for %d jobs", cas, jobs)
	}
	return "", ""
}

// drain clears every fault, replays quarantined or gapped repairs, repairs, and
// then requires the exact end state.
func (r *optRun) drain() (string, string) {
	r.be.setFail(false)
	if id, d := r.replayQuota(); id != "" {
		return id, d
	}
	if id, d := r.replayQuarantined(); id != "" {
		return id, d
	}
	if r.captured == nil {
		return "", ""
	}
	before, id, d := r.deliverView()
	if id != "" {
		return id, d
	}
	for range 6 {
		if i, dd := r.repair(); i != "" {
			return i, dd
		}
	}
	if _, err := r.q.RefreshOptionalSelection(r.ctx, r.ident()); err != nil {
		return "DRAIN", err.Error()
	}
	after, id, d := r.deliverView()
	if id != "" {
		return id, d
	}
	if id, d := r.finalState(before, after); id != "" {
		return id, d
	}
	if id, d := r.checkSpool(true); id != "" {
		return id, d
	}
	return r.casBound()
}

func (r *optRun) deliverView() (optView, string, string) {
	for range 4 {
		if err := r.q.Deliver(r.ctx, r.be, r.ident()); err != nil {
			return optView{}, "BLOCKED", "drain delivery failed: " + err.Error()
		}
	}
	return r.checkRemote()
}

func (r *optRun) quarantinedIDs() ([]string, error) {
	rows, err := r.q.db.QueryContext(r.ctx, `SELECT id FROM optional_repairs WHERE state='quarantined'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *optRun) replayQuarantined() (string, string) {
	ids, err := r.quarantinedIDs()
	if err != nil {
		return "HARNESS", err.Error()
	}
	for _, id := range ids {
		if err := r.q.ReplayOptionalRepair(r.ctx, id); err != nil {
			return "REPLAY", err.Error()
		}
		r.stats.RepairReplays++
	}
	return "", ""
}

// finalState: after repair the selected artifact is exactly the expected bytes
// (or stays withheld when its policy was revoked) and the raw epochs are unchanged.
func (r *optRun) finalState(before, after optView) (string, string) {
	if !slices.EqualFunc(before.rawEpochs, after.rawEpochs, func(a, b publication.Source) bool {
		return a.EpochID == b.EpochID && a.Revision == b.Revision && a.Object == b.Object
	}) {
		return "RAWMOVED", "repair changed the raw epochs"
	}
	switch {
	case r.attached && r.revoked.Load() && (after.selected != 0 || after.withheld == 0):
		return "REVOKED", fmt.Sprintf("revoked artifact selected=%d withheld=%d after repair", after.selected, after.withheld)
	case r.attached && !r.revoked.Load() && (after.selected != 1 || after.withheld != 0):
		return "REPAIR", fmt.Sprintf("artifact not repaired: selected=%d withheld=%d reasons=%v", after.selected, after.withheld, after.reasons)
	}
	return "", ""
}

func TestModelOptionalHistories(t *testing.T) {
	base := t.TempDir()
	var stats optStats
	for i := range optionalHistories {
		seed := uint64(optionalSeed + i)
		h := generateOptionalHistory(i, seed)
		if id, detail := runOptionalHistory(t.Context(), h, base, &stats); id != "" {
			t.Fatalf("history %d seed=%d invariant=%s: %s", i, seed, id, detail)
		}
	}
	t.Logf("optional model coverage: %+v", stats)
	for name, n := range map[string]int{
		"upload fault":             stats.UploadFaults,
		"attachment":               stats.Attached,
		"restart":                  stats.Restarts,
		"unavailable withheld":     stats.WithheldUnavailable,
		"policy withheld":          stats.WithheldPolicy,
		"terminal quota gap":       stats.QuotaGaps,
		"quota replay":             stats.QuotaReplays,
		"repair run":               stats.RepairsRun,
		"repair replay":            stats.RepairReplays,
		"repaired and selected":    stats.Repaired,
		"restore after revocation": stats.Restores,
		"revocation":               stats.Revocations,
		"raw never blocked":        stats.RawNeverBlocked,
		"held payload":             stats.HeldPayloads,
		"cas bound":                stats.CASBoundChecks,
	} {
		if n == 0 {
			t.Errorf("optional model never exercised %q (coverage %+v)", name, stats)
		}
	}
}
