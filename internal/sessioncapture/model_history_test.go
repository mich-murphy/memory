package sessioncapture

// Workload-shaped, seeded model-based history test.
//
// The model is derived from docs/session-capture-contract.md section 1, not from
// implementation output: a proven append advances a source epoch, anything else
// (rewrite, truncation, same-size rewrite) seals it and opens another, sealed
// epochs stay readable, and capture takes the literal file bytes (a partial
// trailing line is part of the bytes). Delivery must publish exactly those
// epochs, never regress the current sequence, never publish an excluded session
// and must retry autonomously once the backoff of a failed attempt has expired.
//
// The generator samples shapes measured on real Claude/Codex/Pi use (see the
// delivery workload report) and scales sizes down so a history stays small:
// heavy-tailed append sizes (lognormal body, rare Pareto tail), line lengths
// with a rare very long line, a burst/idle clock mixture, per-agent source counts
// (Claude subagent and tool-result files up to dozens) and up to 16 concurrent
// sessions. Rewrite probabilities are scaled up from the measured 0.01-5% per
// session so the epoch path is exercised in a small run, with a rare
// many-epoch "storm" session.
//
// Replay a failure with -model-seed=<seed> -histories=1.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/publication"
)

const defaultModelHistories = 40

var (
	modelSeed      = flag.Uint64("model-seed", 20261102, "model history master seed (history i uses seed+i)")
	modelHistories = flag.Int("histories", defaultModelHistories, "number of model histories to run")
)

// ---------- recording, lost-ACK backend ----------

type modelPut struct {
	Key      string
	Raw, Dec []byte
}

type modelBackend struct {
	mu        sync.Mutex
	inner     *publication.Local
	puts      []modelPut
	cas       [][]byte
	casKeys   []string
	writeKeys []string
	lostKind  string
	lostNth   int
	lostFired int
	lastSeq   map[string]uint64
	lastBody  map[string][]byte
	applied   map[string]bool
	violation string
	repeats   int
}

func newModelBackend(root string) *modelBackend {
	return &modelBackend{inner: publication.NewLocal(root), lastSeq: map[string]uint64{}, lastBody: map[string][]byte{}, applied: map[string]bool{}}
}

func (b *modelBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return b.inner.Open(ctx, key)
}

func (b *modelBackend) Current(ctx context.Context, key string) ([]byte, string, error) {
	return b.inner.Current(ctx, key)
}

func (b *modelBackend) Sync(ctx context.Context, key string) error { return b.inner.Sync(ctx, key) }

func (b *modelBackend) lose(kind string) bool { // caller holds mu
	if b.lostKind != kind {
		return false
	}
	b.lostNth--
	if b.lostNth > 0 {
		return false
	}
	b.lostKind = ""
	b.lostFired++
	return true
}

func modelDecode(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		return b
	}
	zr, err := zstd.NewReader(bytes.NewReader(b))
	if err != nil {
		return b
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, 64<<20))
	if err != nil {
		return b
	}
	return out
}

func (b *modelBackend) PutFile(ctx context.Context, key, filename string) error {
	data, readErr := os.ReadFile(filename)
	err := b.inner.PutFile(ctx, key, filename)
	b.mu.Lock()
	defer b.mu.Unlock()
	if readErr != nil {
		b.violation = "recorder could not read upload source: " + readErr.Error()
	} else {
		b.puts = append(b.puts, modelPut{Key: key, Raw: data, Dec: modelDecode(data)})
	}
	if err != nil {
		return err
	}
	b.writeKeys = append(b.writeKeys, key)
	if b.lose("put") {
		return errors.New("model: ack lost after PutFile")
	}
	return nil
}

func (b *modelBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	err := b.inner.CompareAndSwap(ctx, key, token, body)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		return err
	}
	b.writeKeys = append(b.writeKeys, key)
	cp := slices.Clone(body)
	b.cas = append(b.cas, cp)
	b.casKeys = append(b.casKeys, key)
	var c publication.Current
	if json.Unmarshal(body, &c) == nil {
		b.applied[c.PublicationID] = true
		if last, ok := b.lastSeq[key]; ok {
			switch {
			case c.Sequence < last || (c.Sequence == last && !bytes.Equal(b.lastBody[key], body)):
				b.violation = fmt.Sprintf("I3: current sequence %d after %d for %s", c.Sequence, last, key)
			case c.Sequence == last:
				b.repeats++
			}
		}
		b.lastSeq[key] = c.Sequence
		b.lastBody[key] = cp
	}
	if b.lose("cas") {
		return errors.New("model: ack lost after CompareAndSwap")
	}
	return nil
}

// writesUnder counts successful backend writes below one session's key prefix.
func (b *modelBackend) writesUnder(base string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, k := range b.writeKeys {
		if strings.HasPrefix(k, base+"/") {
			n++
		}
	}
	return n
}

func (b *modelBackend) lostCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lostFired
}

// leaks scans keys, metadata (CAS bodies) and raw plus zstd-decoded object bytes.
func (b *modelBackend) leaks(vars [][]byte) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, p := range b.puts {
		for _, v := range vars {
			if bytes.Contains([]byte(p.Key), v) || bytes.Contains(p.Raw, v) || bytes.Contains(p.Dec, v) {
				out = append(out, fmt.Sprintf("put %s contains %q", p.Key, v))
			}
		}
	}
	for i, c := range b.cas {
		for _, v := range vars {
			if bytes.Contains(c, v) || bytes.Contains([]byte(b.casKeys[i]), v) {
				out = append(out, fmt.Sprintf("cas %s contains %q", b.casKeys[i], v))
			}
		}
	}
	return out
}

// ---------- workload-shaped generation ----------

type modelOp struct {
	Kind    string
	S, R    int
	Size    int    `json:",omitempty"`
	Seed    uint64 `json:",omitempty"`
	Partial bool   `json:",omitempty"`
	X       int    `json:",omitempty"`
	Stage   string `json:",omitempty"`
	Nth     int    `json:",omitempty"`
	DurMS   int64  `json:",omitempty"`
}

type modelHistory struct {
	Seed      uint64
	Agents    []string
	Sources   []int
	Denied    []bool
	Rewriters []bool
	Ops       []modelOp
}

const (
	maxModelSessions = 16
	maxModelSources  = 48
	maxModelOps      = 110
	maxModelFaults   = 6
)

var (
	modelCaptureStages = []string{"payload-before-sync", "payload-synced", "job-before-commit"}
	modelPostPublish   = []string{"current-after", "current-published", "ack-before-commit", "ack-committed"}
	modelDeliverStages = []string{"upload-before", "upload-after", "manifest-before", "manifest-upload-before", "manifest-upload-after", "current-before"}
	modelRewriteProb   = map[string]float64{"claude": 0.25, "pi": 0.08, "codex": 0.05}
)

func lognormal(rnd *rand.Rand, median, sigma float64) float64 {
	return median * math.Exp(sigma*rnd.NormFloat64())
}

func pareto(rnd *rand.Rand, xm, alpha float64) float64 {
	return xm / math.Pow(1-rnd.Float64(), 1/alpha)
}

// appendSize: lognormal body with a rare Pareto tail, capped to stay test-sized.
func appendSize(rnd *rand.Rand) int {
	v := lognormal(rnd, 160, 1.3)
	if rnd.Float64() < 0.02 {
		v = pareto(rnd, 4096, 1.2)
	}
	return int(min(max(v, 1), 98304))
}

// lineLength: lognormal lines with a rare very long one (the scaled multi-MB tail).
func lineLength(rnd *rand.Rand) int {
	v := lognormal(rnd, 48, 1.1)
	if rnd.Float64() < 0.005 {
		v = pareto(rnd, 8192, 1.1)
	}
	return int(min(v, 65536))
}

// modelBytes deterministically builds size bytes of newline-delimited text. The
// final byte is a newline unless partial, in which case it never is.
func modelBytes(seed uint64, size int, partial bool) []byte {
	rnd := rand.New(rand.NewPCG(seed, seed^0xa5a5a5a5))
	alphabet := []rune("abcdefghijklmnopqrstuvwxyz0123456789 {}\":,é")
	out := make([]byte, 0, size+64)
	for len(out) < size {
		for range lineLength(rnd) {
			out = utf8.AppendRune(out, alphabet[rnd.IntN(len(alphabet))])
		}
		out = append(out, '\n')
	}
	out = out[:size]
	switch {
	case !partial:
		out[size-1] = '\n'
	case out[size-1] == '\n':
		out[size-1] = 'x'
	}
	return out
}

func sessionCount(rnd *rand.Rand) int {
	switch u := rnd.Float64(); {
	case u < 0.5:
		return 1
	case u < 0.85:
		return 2 + rnd.IntN(3)
	case u < 0.95:
		return 5 + rnd.IntN(4)
	default:
		return 9 + rnd.IntN(maxModelSessions-8)
	}
}

func pickAgent(rnd *rand.Rand) string {
	switch u := rnd.IntN(100); {
	case u < 40:
		return "claude"
	case u < 80:
		return "pi"
	default:
		return "codex"
	}
}

// subagentCount: half of Claude sessions have none; the rest follow a lognormal
// with a long tail (dozens of subagent and tool-result files).
func subagentCount(rnd *rand.Rand) int {
	if rnd.Float64() < 0.5 {
		return 0
	}
	return int(min(lognormal(rnd, 3, 1.0), 23))
}

func clockAdvanceMS(rnd *rand.Rand) int64 {
	var seconds float64
	switch u := rnd.Float64(); {
	case u < 0.7: // burst
		seconds = lognormal(rnd, 1, 1.5)
	case u < 0.95: // working pause
		seconds = 5 + rnd.Float64()*115
	default: // idle gap, up to four hours
		seconds = min(pareto(rnd, 300, 0.6), 14400)
	}
	return max(int64(seconds*1000), 1)
}

func faultStage(rnd *rand.Rand) string {
	switch u := rnd.IntN(100); {
	case u < 20:
		return "job-committed"
	case u < 35:
		return modelCaptureStages[rnd.IntN(len(modelCaptureStages))]
	case u < 65:
		return modelPostPublish[rnd.IntN(len(modelPostPublish))]
	default:
		return modelDeliverStages[rnd.IntN(len(modelDeliverStages))]
	}
}

func generateModelHistory(seed uint64) modelHistory {
	rnd := rand.New(rand.NewPCG(seed, seed*0x9e3779b97f4a7c15+1))
	n := sessionCount(rnd)
	h := modelHistory{Seed: seed}
	budget := maxModelSources
	for s := range n {
		agent := pickAgent(rnd)
		sources := 1
		if agent == "claude" {
			sources += subagentCount(rnd)
		}
		sources = min(sources, budget-(n-s-1))
		budget -= sources
		h.Agents = append(h.Agents, agent)
		h.Sources = append(h.Sources, sources)
		h.Denied = append(h.Denied, rnd.Float64() < 0.05)
		h.Rewriters = append(h.Rewriters, rnd.Float64() < modelRewriteProb[agent])
	}
	storm := -1
	count := 30 + rnd.IntN(31) + 3*n
	if rnd.Float64() < 0.03 { // rare many-epoch session
		storm = rnd.IntN(n)
		h.Rewriters[storm] = true
		count += 20
	}
	count = min(count, maxModelOps)
	touched := map[[2]int]bool{}
	var touchedList [][2]int
	pickSession := func() int {
		if rnd.Float64() < 0.7 {
			return rnd.IntN(max(1, (n+2)/3))
		}
		return rnd.IntN(n)
	}
	pickSource := func(s int) int {
		if h.Agents[s] == "claude" && rnd.Float64() < 0.55 {
			return 0
		}
		return rnd.IntN(h.Sources[s])
	}
	faults := 0
	for range count {
		op := modelOp{S: pickSession()}
		op.R = pickSource(op.S)
		roll := rnd.IntN(100)
		if roll >= 93 && faults >= maxModelFaults {
			roll = 85
		}
		switch {
		case roll < 50:
			op.Seed, op.Size = rnd.Uint64(), appendSize(rnd)
			share := 0.0
			if h.Rewriters[op.S] {
				share = 0.25
				if op.S == storm {
					share = 0.45
				}
			}
			if rnd.Float64() >= share {
				op.Kind, op.Partial = "Append", rnd.IntN(5) == 0
				if op.Partial {
					op.Kind = "AppendPartial"
				}
				break
			}
			op.X = rnd.IntN(1000)
			switch u := rnd.IntN(100); {
			case u < 40:
				op.Kind = "Rewrite"
			case u < 65:
				op.Kind = "SameSizeRewrite"
			case u < 85:
				op.Kind = "Truncate"
			case u < 95:
				op.Kind = "TruncateZero"
			default:
				op.Kind = "Delete"
			}
		case roll < 69:
			if len(touchedList) > 0 && rnd.Float64() < 0.85 {
				t := touchedList[rnd.IntN(len(touchedList))]
				op.S, op.R = t[0], t[1]
			}
			op.Kind = "Capture"
			if roll >= 64 {
				op.Kind = "CaptureIfChanged"
			}
		case roll < 77:
			op.Kind = "Deliver"
		case roll < 82:
			op.Kind = "DeliverDue"
		case roll < 90:
			op.Kind, op.DurMS = "Clock", clockAdvanceMS(rnd)
		case roll < 93:
			op.Kind = "Restart"
		case roll < 97:
			op.Kind, op.Stage, op.Nth = "Fault", faultStage(rnd), 1+rnd.IntN(2)
			faults++
		default:
			op.Kind, op.Stage, op.Nth = "LostAck", []string{"put", "cas"}[rnd.IntN(2)], 1+rnd.IntN(2)
			faults++
		}
		if strings.HasPrefix(op.Kind, "Append") || op.Kind == "Rewrite" {
			if k := [2]int{op.S, op.R}; !touched[k] {
				touched[k] = true
				touchedList = append(touchedList, k)
			}
		}
		h.Ops = append(h.Ops, op)
	}
	if rnd.Float64() < 0.25 { // opt-out in the middle of a history
		at := rnd.IntN(len(h.Ops) + 1)
		h.Ops = slices.Insert(h.Ops, at, modelOp{Kind: "Deny", S: rnd.IntN(n)})
	}
	return h
}

// ---------- model ----------

type modelEpoch struct {
	content []byte
	rev     int
}

type modelSource struct {
	epochs []modelEpoch
	file   []byte
	exists bool
}

// apply is the contract model of one successful capture of bytes b.
func (s *modelSource) apply(b []byte, ifChanged bool) {
	if len(s.epochs) == 0 {
		s.epochs = append(s.epochs, modelEpoch{slices.Clone(b), 1})
		return
	}
	a := &s.epochs[len(s.epochs)-1]
	switch {
	case bytes.Equal(b, a.content):
		if !ifChanged {
			a.rev++
		}
	case bytes.HasPrefix(b, a.content):
		a.content = slices.Clone(b)
		a.rev++
	default:
		s.epochs = append(s.epochs, modelEpoch{slices.Clone(b), 1})
	}
}

// ---------- runner ----------

type modelStats struct {
	Histories, Ops, CaptureOK, CaptureFaultAccepted, CaptureFaultRejected int
	DeliverOK, DeliverFailed, Restarts, FaultsFired, LostAcks, NewEpochs   int
	RepeatedCAS, AlreadyApplied, BackoffGated, DueDelivered                int
	PolicyRefused, MultiEpochSessions                                      int
}

type modelRun struct {
	ctx        context.Context
	h          modelHistory
	dir        string
	be         *modelBackend
	q          *Queue
	opts       Options
	src        [][]*modelSource
	stats      *modelStats
	mu         sync.Mutex
	now        time.Time
	denied     []bool
	denyWrites []int
	armed      bool
	stage      string
	nth        int
	seen       int
	fired      string
	total      int
	exports    int
}

func (r *modelRun) hook(s string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.armed && s == r.stage {
		r.seen++
		if r.seen >= r.nth {
			r.armed = false
			r.fired = s
			r.total++
			return errors.New("model-fault:" + s)
		}
	}
	return nil
}

func (r *modelRun) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *modelRun) firedCounts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total, r.be.lostCount()
}

func (r *modelRun) isDenied(s int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.denied[s]
}

func (r *modelRun) path(s, i int) string {
	return filepath.Join(r.dir, "src", fmt.Sprintf("s%d", s), fmt.Sprintf("r%d.jsonl", i))
}

func (r *modelRun) ident(s int) publication.Identity {
	return publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: r.h.Agents[s], SessionID: fmt.Sprintf("sess%d", s)}
}

func (r *modelRun) role(s, i int) string {
	switch {
	case r.h.Agents[s] != "claude":
		return "transcript"
	case i == 0:
		return "main"
	case i%3 == 0:
		return "tool-result"
	default:
		return "subagent"
	}
}

func (r *modelRun) req(s, i int) Request {
	return Request{Identity: r.ident(s), SourceID: fmt.Sprintf("src%d", i), Role: r.role(s, i), Path: r.path(s, i), ObservedAtNS: 1}
}

func (r *modelRun) openQueue() error {
	q, err := Open(r.ctx, filepath.Join(r.dir, "queue"), r.opts)
	if err != nil {
		return err
	}
	r.q = q
	return nil
}

func runModelHistory(ctx context.Context, h modelHistory, base string, stats *modelStats) (string, string) {
	dir, err := os.MkdirTemp(base, "h-")
	if err != nil {
		return "HARNESS", err.Error()
	}
	defer func() { _ = os.RemoveAll(dir) }()
	r := &modelRun{ctx: ctx, h: h, dir: dir, stats: stats, now: time.Unix(1_700_000_000, 0)}
	return r.run()
}

func (r *modelRun) run() (id, detail string) {
	defer func() {
		if r.q != nil {
			_ = r.q.Close()
		}
	}()
	r.be = newModelBackend(filepath.Join(r.dir, "remote"))
	if err := publication.WriteArchive(r.ctx, r.be.inner, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
		return "HARNESS", err.Error()
	}
	r.denied = slices.Clone(r.h.Denied)
	r.denyWrites = make([]int, len(r.h.Agents))
	r.opts = Options{
		ArchiveID: "archive", MaxSpoolBytes: 1 << 30, Now: r.clock, Fault: r.hook,
		CheckPolicy: func(_ context.Context, req Request) error {
			var s int
			if _, err := fmt.Sscanf(req.Identity.SessionID, "sess%d", &s); err != nil {
				return err
			}
			if r.isDenied(s) {
				return errors.Join(ErrPolicy, ErrSessionExcluded)
			}
			return nil
		},
	}
	for s := range r.h.Agents {
		row := make([]*modelSource, r.h.Sources[s])
		for i := range row {
			row[i] = &modelSource{}
		}
		r.src = append(r.src, row)
		if err := os.MkdirAll(filepath.Dir(r.path(s, 0)), 0o700); err != nil {
			return "HARNESS", err.Error()
		}
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
		if id, d := r.checkInvariants(); id != "" {
			return id, fmt.Sprintf("after op %d %+v: %s", i, op, d)
		}
	}
	return r.drain()
}

// checkInvariants runs after every operation: a current root only ever advances,
// and nothing is written for a session once it is excluded.
func (r *modelRun) checkInvariants() (string, string) {
	r.be.mu.Lock()
	violation := r.be.violation
	r.be.mu.Unlock()
	if violation != "" {
		return "I3", violation
	}
	for s := range r.denied {
		if r.isDenied(s) {
			if w := r.be.writesUnder(r.ident(s).BaseKey()); w != r.denyWrites[s] {
				return "PRIVACY", fmt.Sprintf("session %d wrote %d backend objects after exclusion", s, w-r.denyWrites[s])
			}
		}
	}
	return "", ""
}

func (r *modelRun) mutateFile(op modelOp, s *modelSource, p string) (string, string) {
	var err error
	switch op.Kind {
	case "Append", "AppendPartial":
		data := modelBytes(op.Seed, op.Size, op.Kind == "AppendPartial")
		var f *os.File
		if f, err = os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, err = f.Write(data)
			err = errors.Join(err, f.Close())
		}
		s.file, s.exists = append(s.file, data...), true
	case "Rewrite":
		data := modelBytes(op.Seed, op.Size, false)
		err = os.WriteFile(p, data, 0o600)
		s.file, s.exists = data, true
	case "SameSizeRewrite":
		if !s.exists || len(s.file) == 0 {
			return "", ""
		}
		b := slices.Clone(s.file)
		b[op.X%len(b)] ^= 0x01
		err = os.WriteFile(p, b, 0o600)
		s.file = b
	case "Truncate", "TruncateZero":
		if !s.exists {
			return "", ""
		}
		n := 0
		if op.Kind == "Truncate" && len(s.file) > 0 {
			n = op.X % len(s.file)
		}
		err = os.Truncate(p, int64(n))
		s.file = s.file[:n]
	case "Delete":
		if s.exists {
			err = os.Remove(p)
		}
		s.exists, s.file = false, nil
	}
	if err != nil {
		return "HARNESS", err.Error()
	}
	return "", ""
}

func (r *modelRun) captureStep(op modelOp, s *modelSource) (string, string) {
	if !s.exists {
		return "", ""
	}
	b := slices.Clone(s.file)
	r.mu.Lock()
	r.fired = ""
	r.mu.Unlock()
	before := len(s.epochs)
	ifChanged := op.Kind == "CaptureIfChanged"
	var err error
	if ifChanged {
		_, err = r.q.CaptureIfChanged(r.ctx, r.req(op.S, op.R))
	} else {
		_, err = r.q.Capture(r.ctx, r.req(op.S, op.R))
	}
	r.mu.Lock()
	fired := r.fired
	r.mu.Unlock()
	switch {
	case err == nil:
		s.apply(b, ifChanged)
		r.stats.CaptureOK++
	case fired == "" && r.isDenied(op.S) && errors.Is(err, ErrPolicy):
		r.stats.PolicyRefused++
	case fired == "job-committed": // failure after the durable job commit still owns the bytes
		s.apply(b, ifChanged)
		r.stats.CaptureFaultAccepted++
	case fired != "":
		r.stats.CaptureFaultRejected++
	default:
		return "CAP", "unexpected capture error without injected fault: " + err.Error()
	}
	if len(s.epochs) > before {
		r.stats.NewEpochs++
	}
	return "", ""
}

func (r *modelRun) step(op modelOp) (string, string) {
	s := r.src[op.S][op.R]
	switch op.Kind {
	case "Append", "AppendPartial", "Rewrite", "SameSizeRewrite", "Truncate", "TruncateZero", "Delete":
		return r.mutateFile(op, s, r.path(op.S, op.R))
	case "Capture", "CaptureIfChanged":
		return r.captureStep(op, s)
	case "Restart":
		if err := r.q.Close(); err != nil {
			return "HARNESS", "close: " + err.Error()
		}
		r.q = nil
		if err := r.openQueue(); err != nil {
			return "RESTART", "reopen: " + err.Error()
		}
		r.stats.Restarts++
	case "Fault":
		r.mu.Lock()
		r.armed, r.stage, r.nth, r.seen = true, op.Stage, op.Nth, 0
		r.mu.Unlock()
	case "LostAck":
		r.be.mu.Lock()
		r.be.lostKind, r.be.lostNth = op.Stage, op.Nth
		r.be.mu.Unlock()
	case "Clock":
		r.mu.Lock()
		r.now = r.now.Add(time.Duration(op.DurMS) * time.Millisecond)
		r.mu.Unlock()
	case "Deny":
		r.mu.Lock()
		r.denied[op.S] = true
		r.mu.Unlock()
		r.denyWrites[op.S] = r.be.writesUnder(r.ident(op.S).BaseKey())
	case "Deliver":
		return r.deliverStep()
	case "DeliverDue":
		return r.dueStep(op.S)
	}
	return "", ""
}

// pendingHead is the oldest job of a session that is neither published nor superseded.
func (r *modelRun) pendingHead(s int) (Status, bool, error) {
	st, err := r.q.Status(r.ctx)
	if err != nil {
		return Status{}, false, err
	}
	var head Status
	found := false
	for _, j := range st {
		if j.Identity != r.ident(s) || j.State == "published" || j.State == "superseded" {
			continue
		}
		if !found || j.Sequence < head.Sequence {
			head, found = j, true
		}
	}
	return head, found, nil
}

// noteAlreadyApplied counts a pending head whose publication the remote current
// already holds (lost ACK): delivery must reconcile it, not publish it again.
func (r *modelRun) noteAlreadyApplied(s int) error {
	head, ok, err := r.pendingHead(s)
	if err != nil || !ok {
		return err
	}
	body, _, err := r.be.inner.Current(r.ctx, r.ident(s).CurrentKey())
	if errors.Is(err, publication.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var c publication.Current
	if err := json.Unmarshal(body, &c); err != nil {
		return err
	}
	if c.PublicationID == head.PublicationID {
		r.stats.AlreadyApplied++
	}
	return nil
}

func (r *modelRun) deliverAll(skipDenied bool) []error {
	errs := make([]error, len(r.h.Agents))
	for s := range errs {
		if skipDenied && r.isDenied(s) {
			continue
		}
		errs[s] = r.q.Deliver(r.ctx, r.be, r.ident(s))
	}
	return errs
}

func (r *modelRun) deliverStep() (string, string) {
	for s := range r.h.Agents {
		if err := r.noteAlreadyApplied(s); err != nil {
			return "HARNESS", err.Error()
		}
	}
	t0, l0 := r.firedCounts()
	errs := r.deliverAll(false)
	t1, l1 := r.firedCounts()
	r.stats.FaultsFired += t1 - t0
	r.stats.LostAcks += l1 - l0
	failed := false
	for s, e := range errs {
		if e == nil {
			continue
		}
		if r.isDenied(s) && errors.Is(e, ErrPolicy) {
			r.stats.PolicyRefused++
			continue
		}
		failed = true
		if t1 == t0 && l1 == l0 {
			return "DELIVER", fmt.Sprintf("session %d unexpected deliver error without fault: %v", s, e)
		}
		if id, d := r.checkReadable(s); id != "" {
			return id, d
		}
	}
	if failed {
		r.stats.DeliverFailed++
		return "", ""
	}
	r.stats.DeliverOK++
	if id, d := r.checkRemote("I1"); id != "" {
		return id, d
	}
	return r.checkIdempotent()
}

// dueStep checks the retry/backoff contract on the injected clock: a head still
// inside its backoff window is not attempted (no error, no backend write).
func (r *modelRun) dueStep(s int) (string, string) {
	if err := r.noteAlreadyApplied(s); err != nil {
		return "HARNESS", err.Error()
	}
	head, ok, err := r.pendingHead(s)
	if err != nil {
		return "HARNESS", err.Error()
	}
	blocked := ok && head.State == "retry" && head.NextRetryNS > r.clock().UnixNano()
	base := r.ident(s).BaseKey()
	w0 := r.be.writesUnder(base)
	t0, l0 := r.firedCounts()
	err = r.q.DeliverDue(r.ctx, r.be, r.ident(s))
	t1, l1 := r.firedCounts()
	r.stats.FaultsFired += t1 - t0
	r.stats.LostAcks += l1 - l0
	w1 := r.be.writesUnder(base)
	switch {
	case blocked:
		if err != nil || w1 != w0 {
			return "BACKOFF", fmt.Sprintf("head seq %d retry at %d attempted before due (now %d): err=%v writes=%d", head.Sequence, head.NextRetryNS, r.clock().UnixNano(), err, w1-w0)
		}
		r.stats.BackoffGated++
	case err != nil:
		if r.isDenied(s) && errors.Is(err, ErrPolicy) {
			r.stats.PolicyRefused++
			return "", ""
		}
		if t1 == t0 && l1 == l0 {
			return "DUE", fmt.Sprintf("session %d due delivery failed without fault: %v", s, err)
		}
		return r.checkReadable(s)
	case ok && w1 > w0:
		r.stats.DueDelivered++
	}
	return "", ""
}

// checkReadable (I7): after a failed delivery the current root is absent
// (nothing ever applied) or one that was previously applied.
func (r *modelRun) checkReadable(s int) (string, string) {
	m, err := publication.Read(r.ctx, r.be.inner, r.ident(s))
	r.be.mu.Lock()
	defer r.be.mu.Unlock()
	_, ever := r.be.lastSeq[r.ident(s).CurrentKey()]
	switch {
	case errors.Is(err, publication.ErrNotFound) && !ever:
		return "", ""
	case err != nil:
		return "I7", fmt.Sprintf("session %d unreadable after failed delivery (ever published=%v): %v", s, ever, err)
	case !r.be.applied[m.PublicationID]:
		return "I7", fmt.Sprintf("session %d read root %s never applied", s, m.PublicationID)
	}
	return "", ""
}

func modelSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// checkRemote (I1/I2): per epoch, the reader's body equals the model bytes
// exactly and the retained epoch set equals the model's. Excluded sessions are
// covered by the privacy invariant instead.
func (r *modelRun) checkRemote(id string) (string, string) {
	for s := range r.h.Agents {
		if r.isDenied(s) {
			continue
		}
		total := 0
		for _, sr := range r.src[s] {
			total += len(sr.epochs)
		}
		if total == 0 {
			continue
		}
		r.exports++
		dest := filepath.Join(r.dir, fmt.Sprintf("exp%d", r.exports))
		exp, err := publication.ExportSession(r.ctx, r.be.inner, r.ident(s), dest)
		if err != nil {
			return id, fmt.Sprintf("session %d export: %v", s, err)
		}
		var selected []publication.Source
		for _, x := range exp.Manifest.Sources {
			if publication.IsSelected(x) {
				selected = append(selected, x)
			}
		}
		if len(selected) != total {
			return id, fmt.Sprintf("session %d retained epochs %d, model %d", s, len(selected), total)
		}
		if detail := r.compareSession(s, selected, exp, dest); detail != "" {
			return id, detail
		}
		_ = os.RemoveAll(dest)
	}
	return "", ""
}

func (r *modelRun) compareSession(s int, selected []publication.Source, exp publication.Export, dest string) string {
	seen := map[string]bool{}
	for i, sr := range r.src[s] {
		var got []publication.Source
		for _, x := range selected {
			if x.SourceID == fmt.Sprintf("src%d", i) {
				got = append(got, x)
			}
		}
		if len(got) != len(sr.epochs) {
			return fmt.Sprintf("session %d src%d epochs %d, model %d", s, i, len(got), len(sr.epochs))
		}
		for k, x := range got {
			want := sr.epochs[k]
			state := "sealed"
			if k == len(got)-1 {
				state = "active"
			}
			if seen[x.EpochID] {
				return "duplicate epoch id"
			}
			seen[x.EpochID] = true
			if x.State != state || x.Revision != uint64(want.rev) || x.Object.Size != int64(len(want.content)) || x.Object.SHA256 != modelSum(want.content) {
				return fmt.Sprintf("session %d src%d epoch %d: state=%s rev=%d size=%d want state=%s rev=%d size=%d", s, i, k, x.State, x.Revision, x.Object.Size, state, want.rev, len(want.content))
			}
			idx := slices.IndexFunc(exp.Bodies, func(b publication.Body) bool { return b.EpochID == x.EpochID && b.SourceID == x.SourceID })
			if idx < 0 {
				return "missing exported body for epoch"
			}
			data, err := os.ReadFile(filepath.Join(dest, exp.Bodies[idx].Path))
			if err != nil || !bytes.Equal(data, want.content) {
				return fmt.Sprintf("session %d src%d epoch %d body (%d bytes, err %v) != model (%d bytes)", s, i, k, len(data), err, len(want.content))
			}
		}
	}
	return ""
}

// checkIdempotent (I4): a second Deliver writes nothing and leaves current unchanged.
func (r *modelRun) checkIdempotent() (string, string) {
	current := func() []string {
		var out []string
		for s := range r.h.Agents {
			b, _, err := r.be.inner.Current(r.ctx, r.ident(s).CurrentKey())
			out = append(out, string(b)+fmt.Sprint(err))
		}
		return out
	}
	total := func() int {
		n := 0
		for s := range r.h.Agents {
			n += r.be.writesUnder(r.ident(s).BaseKey())
		}
		return n
	}
	w0, c0 := total(), current()
	for s, e := range r.deliverAll(true) {
		if e != nil {
			return "I4", fmt.Sprintf("second deliver session %d failed: %v", s, e)
		}
	}
	if w1 := total(); w1 != w0 || !slices.Equal(c0, current()) {
		return "I4", fmt.Sprintf("second deliver wrote %d backend objects / changed current", w1-w0)
	}
	return "", ""
}

// dueDrain: once the injected clock is past the 5 minute retry cap, delivery by
// DeliverDue alone must publish everything pending. No explicit Deliver, no hook.
func (r *modelRun) dueDrain() (string, string) {
	st, err := r.q.Status(r.ctx)
	if err != nil {
		return "HARNESS", err.Error()
	}
	for pass := 0; pass <= len(st)+1; pass++ {
		for s := range r.h.Agents {
			if r.isDenied(s) {
				continue
			}
			if err := r.q.DeliverDue(r.ctx, r.be, r.ident(s)); err != nil {
				return "LIVENESS", fmt.Sprintf("session %d autonomous delivery after backoff failed: %v", s, err)
			}
		}
	}
	for s := range r.h.Agents {
		if r.isDenied(s) {
			continue
		}
		if head, ok, err := r.pendingHead(s); err != nil {
			return "HARNESS", err.Error()
		} else if ok {
			return "LIVENESS", fmt.Sprintf("session %d seq %d still %s (attempts %d, last error %q) after backoff expired", s, head.Sequence, head.State, head.Attempts, head.LastError)
		}
	}
	return "", ""
}

func (r *modelRun) drain() (string, string) {
	r.mu.Lock()
	r.armed = false
	r.now = r.now.Add(10 * time.Minute)
	r.mu.Unlock()
	r.be.mu.Lock()
	r.be.lostKind = ""
	r.be.mu.Unlock()
	if r.h.Seed%2 == 1 { // I2: accepted bytes must not depend on the original source surviving
		for s := range r.src {
			for i, sr := range r.src[s] {
				if sr.exists {
					_ = os.Remove(r.path(s, i))
				}
			}
		}
	}
	if err := r.q.Close(); err != nil {
		return "HARNESS", err.Error()
	}
	r.q = nil
	if err := r.openQueue(); err != nil {
		return "RESTART", "final reopen: " + err.Error()
	}
	if id, d := r.dueDrain(); id != "" {
		return id, d
	}
	for s, e := range r.deliverAll(true) {
		if e != nil {
			return "I2", fmt.Sprintf("fault-free drain failed for session %d: %v", s, e)
		}
	}
	if id, d := r.checkRemote("I2"); id != "" {
		return id, "final drain: " + d
	}
	if id, d := r.checkIdempotent(); id != "" {
		return id, "final drain: " + d
	}
	if id, d := r.checkInvariants(); id != "" {
		return id, d
	}
	r.stats.RepeatedCAS += r.be.repeats
	for s := range r.src {
		epochs := 0
		for _, sr := range r.src[s] {
			epochs += len(sr.epochs)
		}
		if epochs > len(r.src[s]) {
			r.stats.MultiEpochSessions++
		}
	}
	return "", ""
}

func shrinkModelHistory(ctx context.Context, h modelHistory, id, base string) modelHistory {
	budget := 200
	for changed := true; changed && budget > 0; {
		changed = false
		for i := 0; i < len(h.Ops) && budget > 0; i++ {
			budget--
			c := h
			c.Ops = slices.Delete(slices.Clone(h.Ops), i, i+1)
			if got, _ := runModelHistory(ctx, c, base, &modelStats{}); got == id {
				h, changed = c, true
				i--
			}
		}
	}
	return h
}

func TestModelHistories(t *testing.T) {
	base := t.TempDir()
	start := time.Now()
	var stats modelStats
	fails, ran := 0, 0
	for i := range *modelHistories {
		seed := *modelSeed + uint64(i)
		h := generateModelHistory(seed)
		ran++
		id, detail := runModelHistory(t.Context(), h, base, &stats)
		if id == "" {
			continue
		}
		fails++
		m := shrinkModelHistory(t.Context(), h, id, base)
		_, md := runModelHistory(t.Context(), m, base, &modelStats{})
		js, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		t.Errorf("history seed=%d invariant=%s\n  detail: %s\n  original ops=%d minimized ops=%d\n  minimized detail: %s\n  minimized history (replay: -model-seed=%d -histories=1): %s", seed, id, detail, len(h.Ops), len(m.Ops), md, seed, js)
		if fails >= 2 {
			break
		}
	}
	elapsed := time.Since(start)
	t.Logf("model: master=%d histories=%d failures=%d elapsed=%s", *modelSeed, ran, fails, elapsed.Round(time.Millisecond))
	t.Logf("model coverage: %+v", stats)
	if fails > 0 || ran < defaultModelHistories {
		return
	}
	// The default run must reach the paths the model exists to test.
	for name, n := range map[string]int{
		"lost-ACK republish reconciled": stats.AlreadyApplied,
		"job-committed fault accepted":  stats.CaptureFaultAccepted,
		"backoff-gated due delivery":    stats.BackoffGated,
		"due delivery published":        stats.DueDelivered,
		"policy refusal":                stats.PolicyRefused,
		"multi-epoch session":           stats.MultiEpochSessions,
		"restart":                       stats.Restarts,
		"fault fired":                   stats.FaultsFired,
		"lost ack fired":                stats.LostAcks,
	} {
		if n == 0 {
			t.Errorf("default run never exercised %q (coverage %+v)", name, stats)
		}
	}
}

// TestModelGeneratorShape keeps the generator honest about the measured workload
// and deterministic for seed replay.
func TestModelGeneratorShape(t *testing.T) {
	if diff := cmp.Diff(generateModelHistory(7), generateModelHistory(7)); diff != "" {
		t.Fatalf("same seed generated different histories:\n%s", diff)
	}
	var (
		sessions, multi, sources, maxSources, claudeSessions, big, bigLines int
		maxConcurrent                                                       int
		kinds                                                               = map[string]int{}
	)
	const histories = 600
	for seed := range uint64(histories) {
		h := generateModelHistory(seed)
		maxConcurrent = max(maxConcurrent, len(h.Agents))
		total := 0
		for s, a := range h.Agents {
			sessions++
			sources += h.Sources[s]
			total += h.Sources[s]
			if a != "claude" && h.Sources[s] != 1 {
				t.Fatalf("seed %d: %s session has %d sources, want 1", seed, a, h.Sources[s])
			}
			if a == "claude" {
				claudeSessions++
				maxSources = max(maxSources, h.Sources[s])
				if h.Sources[s] > 1 {
					multi++
				}
			}
		}
		if total > maxModelSources || len(h.Ops) > maxModelOps+1 {
			t.Fatalf("seed %d: %d sources, %d ops exceed the test-size caps", seed, total, len(h.Ops))
		}
		for _, op := range h.Ops {
			kinds[op.Kind]++
			if op.Size >= 4096 {
				big++
			}
			if op.Size >= 8192 && bytes.Contains(modelBytes(op.Seed, op.Size, false), bytes.Repeat([]byte("a"), 1)) {
				for _, line := range bytes.Split(modelBytes(op.Seed, op.Size, false), []byte("\n")) {
					if len(line) >= 4096 {
						bigLines++
						break
					}
				}
			}
		}
	}
	if maxConcurrent != maxModelSessions {
		t.Errorf("largest session count %d, want %d", maxConcurrent, maxModelSessions)
	}
	if maxSources < 12 || maxSources > 24 {
		t.Errorf("largest Claude source count %d, want a tail of dozens (12..24)", maxSources)
	}
	if multi*4 < claudeSessions || multi*4 > claudeSessions*3 {
		t.Errorf("%d of %d Claude sessions have subagent files, want roughly half", multi, claudeSessions)
	}
	if big == 0 || bigLines == 0 {
		t.Errorf("heavy tail missing: %d appends >= 4 KiB, %d with a line >= 4 KiB", big, bigLines)
	}
	for _, k := range []string{"Append", "AppendPartial", "Rewrite", "SameSizeRewrite", "Truncate", "TruncateZero", "Delete", "Capture", "CaptureIfChanged", "Deliver", "DeliverDue", "Clock", "Restart", "Fault", "LostAck", "Deny"} {
		if kinds[k] == 0 {
			t.Errorf("operation %s is never generated", k)
		}
	}
}
