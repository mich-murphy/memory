package sessioncapture

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	discoveryHintContract   = "session-discovery-hint/v1"
	maxDiscoveryHintBytes   = 1024
	maxDiscoveryHintEntries = 64
)

var discoveryHintAgents = [...]string{"claude", "codex", "pi"}

type discoveryHint struct {
	Contract     string `json:"contract"`
	Archive      string `json:"archive"`
	Machine      string `json:"machine"`
	Agent        string `json:"agent"`
	Nonce        string `json:"nonce"`
	ObservedAtNS int64  `json:"observed_at_ns"`
}

func validDiscoveryIdentity(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func validDiscoveryAgent(agent string) bool {
	for _, known := range discoveryHintAgents {
		if agent == known {
			return true
		}
	}
	return false
}

// WriteDiscoveryHint durably coalesces a metadata-only discovery observation for
// one native agent. It never opens the Queue or waits for its lifetime lock, and
// owns no source bytes. A successful call is not captured/published progress.
func WriteDiscoveryHint(ctx context.Context, spool, archive, machine, agent string) error {
	if !validDiscoveryIdentity(archive) || !validDiscoveryIdentity(machine) || !validDiscoveryAgent(agent) {
		return errors.New("sessioncapture: invalid discovery hint identity")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	hint := discoveryHint{Contract: discoveryHintContract, Archive: archive, Machine: machine, Agent: agent, Nonce: hex.EncodeToString(nonce[:]), ObservedAtNS: time.Now().UnixNano()}
	body, err := json.Marshal(hint)
	if err != nil {
		return err
	}
	if len(body) > maxDiscoveryHintBytes {
		return ErrQuota
	}
	_, lock, err := lockInbox(ctx, spool)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	dir := filepath.Join(spool, "discovery-hints")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := privateInboxPath(dir, true); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wake-")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, agent+".json")); err != nil {
		return err
	}
	return syncQueueTree(dir)
}

func readDiscoveryHint(path, agent, archive string) (discoveryHint, error) {
	var hint discoveryHint
	if err := privateInboxPath(path, false); err != nil {
		return hint, err
	}
	file, err := os.Open(path)
	if err != nil {
		return hint, err
	}
	body, err := io.ReadAll(io.LimitReader(file, maxDiscoveryHintBytes+1))
	err = errors.Join(err, file.Close())
	if err != nil {
		return hint, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) > maxDiscoveryHintBytes || decoder.Decode(&hint) != nil || decoder.Decode(new(any)) != io.EOF {
		return hint, errInvalidHint
	}
	nonce, err := hex.DecodeString(hint.Nonce)
	if err != nil || len(nonce) != 16 || hint.Contract != discoveryHintContract || hint.Archive != archive || hint.Agent != agent || !validDiscoveryIdentity(hint.Archive) || !validDiscoveryIdentity(hint.Machine) || hint.ObservedAtNS <= 0 {
		return hint, errInvalidHint
	}
	return hint, nil
}

type discoveryHintScan struct {
	hints     map[string]discoveryHint
	rejected  int
	truncated bool
}

// scanDiscoveryHintsLocked always examines the three known slots separately, so
// a bounded page of malicious filenames cannot starve a valid agent's wake.
func scanDiscoveryHintsLocked(spool, archive string) (discoveryHintScan, error) {
	scan := discoveryHintScan{hints: make(map[string]discoveryHint)}
	dir := filepath.Join(spool, "discovery-hints")
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return scan, nil
	}
	if err := privateInboxPath(dir, true); err != nil {
		return scan, err
	}
	f, err := os.Open(dir)
	if err != nil {
		return scan, err
	}
	entries, err := f.ReadDir(maxDiscoveryHintEntries + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return scan, err
	}
	scan.truncated = len(entries) > maxDiscoveryHintEntries
	for _, entry := range entries {
		if !validDiscoveryAgent(strings.TrimSuffix(entry.Name(), ".json")) || !strings.HasSuffix(entry.Name(), ".json") {
			scan.rejected++
		}
	}
	for _, agent := range discoveryHintAgents {
		hint, err := readDiscoveryHint(filepath.Join(dir, agent+".json"), agent, archive)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			scan.rejected++
			continue
		}
		scan.hints[agent] = hint
	}
	return scan, nil
}

func (q *Queue) scanDiscoveryHints(ctx context.Context) (discoveryHintScan, error) {
	_, lock, err := lockInbox(ctx, q.dir)
	if err != nil {
		return discoveryHintScan{}, err
	}
	defer func() { _ = lock.Unlock() }()
	return scanDiscoveryHintsLocked(q.dir, q.opts.ArchiveID)
}

func (q *Queue) pollDiscoveryHints(ctx context.Context) error {
	scan, err := q.scanDiscoveryHints(ctx)
	if err != nil {
		return err
	}
	r := q.runtimeLocal()
	r.mu.Lock()
	if r.wakeSeen == nil {
		r.wakeSeen = make(map[string]string)
	}
	for agent, hint := range scan.hints {
		if r.wakeSeen[agent] != hint.Nonce {
			r.wakeSeen[agent] = hint.Nonce
			select {
			case r.discoveryWake <- struct{}{}:
			default:
			}
		}
	}
	r.mu.Unlock()
	_, err = q.db.ExecContext(ctx, `UPDATE runtime_discovery_wakes SET pending=?,rejected=?,truncated=?,scanned_at=? WHERE id=1`, len(scan.hints), scan.rejected, scan.truncated, q.opts.Now().UnixNano())
	return err
}

// ackDiscoveryHints runs only after a complete consistent sweep commits. Matching
// nonces fence replacement: a later observation must survive the earlier pass.
func (q *Queue) ackDiscoveryHints(ctx context.Context, snapshot map[string]discoveryHint) error {
	_, lock, err := lockInbox(ctx, q.dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	scan, err := scanDiscoveryHintsLocked(q.dir, q.opts.ArchiveID)
	if err != nil {
		return err
	}
	var changed bool
	for agent, hint := range scan.hints {
		if previous, ok := snapshot[agent]; ok && previous.Nonce == hint.Nonce {
			if err := os.Remove(filepath.Join(q.dir, "discovery-hints", agent+".json")); err != nil {
				return err
			}
			changed = true
		} else {
			select {
			case q.runtimeLocal().discoveryWake <- struct{}{}:
			default:
			}
		}
	}
	if changed {
		return syncDir(filepath.Join(q.dir, "discovery-hints"))
	}
	return nil
}

// beginDiscoveryHints pins the observations preceding the first bounded batch.
// A wake arriving during a partial cursor belongs to the next complete sweep.
func (q *Queue) beginDiscoveryHints(ctx context.Context) error {
	r := q.runtimeLocal()
	if r.wakeSnapshot != nil {
		return nil
	}
	select {
	case <-r.discoveryWake:
	default:
	}
	scan, err := q.scanDiscoveryHints(ctx)
	if err != nil {
		return err
	}
	r.wakeSnapshot = scan.hints
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wakeSeen == nil {
		r.wakeSeen = make(map[string]string)
	}
	for agent, hint := range scan.hints {
		r.wakeSeen[agent] = hint.Nonce
	}
	return nil
}

func (q *Queue) finishDiscoveryHints(ctx context.Context, finished bool) error {
	// Legacy Discover cannot prove completion; its durable wakes remain pending.
	if !finished {
		return nil
	}
	var complete bool
	if err := q.db.QueryRowContext(ctx, "SELECT complete FROM runtime_discovery WHERE id=1").Scan(&complete); err != nil {
		return err
	}
	r := q.runtimeLocal()
	if complete {
		if err := q.ackDiscoveryHints(ctx, r.wakeSnapshot); err != nil {
			return err
		}
	}
	r.wakeSnapshot = nil
	return nil
}
