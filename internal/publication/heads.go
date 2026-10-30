package publication

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// SessionNamespaceLister is an optional backend capability. Results are immediate
// session common-prefixes, ordered strictly after after, never object/history keys.
// A call examines at most limit namespaces. complete applies to this agent only.
type SessionNamespaceLister interface {
	ListSessionNamespaces(ctx context.Context, scope Identity, after string, limit int) (prefixes []string, complete bool, err error)
}

var ErrListingUnsupported = errors.New("publication backend does not support current-root enumeration")

type PublicationHead struct {
	Identity      Identity `json:"identity"`
	Sequence      uint64   `json:"sequence"`
	PublicationID string   `json:"publication_id"`
}
type PublicationHeadFailure struct {
	Identity Identity `json:"identity"`
	Reason   string   `json:"reason"`
}

type PublicationHeads struct {
	Failures   []PublicationHeadFailure `json:"failures,omitempty"`
	Contract   string                   `json:"contract"`
	ArchiveID  string                   `json:"archive_id"`
	Heads      []PublicationHead        `json:"heads"`
	NextCursor string                   `json:"next_cursor"`
	Complete   bool                     `json:"complete"`
}

// CurrentHead validates the committed reference only. Closure is verified by
// pinned export, not by discovery. It deliberately does not read the manifest.
func CurrentHead(ctx context.Context, b Backend, id Identity) (PublicationHead, error) {
	if !validIdentity(id) {
		return PublicationHead{}, errors.New("invalid head identity")
	}
	data, _, err := b.Current(ctx, id.CurrentKey())
	if err != nil {
		return PublicationHead{}, err
	}
	c, err := parseCurrent(data, id)
	if err != nil {
		return PublicationHead{}, err
	}
	return PublicationHead{Identity: id, Sequence: c.Sequence, PublicationID: c.PublicationID}, nil
}

// SessionPrefix is the exact canonical subtree allowed for scoped LIST requests.
func SessionPrefix(scope Identity) (string, error) {
	if !validIdentity(Identity{ArchiveID: scope.ArchiveID, MachineID: scope.MachineID, Agent: scope.Agent, SessionID: "scope"}) || !supportedHeadAgent(scope.Agent) {
		return "", errors.New("invalid current-root scope")
	}
	base := (Identity{ArchiveID: scope.ArchiveID, MachineID: scope.MachineID, Agent: scope.Agent, SessionID: "scope"}).BaseKey()
	prefix := base[:strings.LastIndex(base, "/")+1]
	if len(prefix) > 800 {
		return "", errors.New("current-root scope exceeds limit")
	}
	return prefix, nil
}
func supportedHeadAgent(s string) bool { return s == "claude" || s == "codex" || s == "pi" }

// SessionIdentity rejects aliases, non-UTF8 identities, and out-of-scope prefixes.
func SessionIdentity(scope Identity, prefix string) (Identity, error) {
	base, err := SessionPrefix(scope)
	if err != nil {
		return Identity{}, err
	}
	if len(prefix) > 1024 || !strings.HasPrefix(prefix, base) || !strings.HasSuffix(prefix, "/") {
		return Identity{}, errors.New("invalid session namespace")
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(prefix, base), "/")
	raw, err := hex.DecodeString(encoded)
	id := Identity{ArchiveID: scope.ArchiveID, MachineID: scope.MachineID, Agent: scope.Agent, SessionID: string(raw)}
	if err != nil || !validIdentity(id) || id.BaseKey()+"/" != prefix {
		return Identity{}, errors.New("invalid session namespace")
	}
	return id, nil
}

type headCursor struct {
	Version int    `json:"v"`
	Archive string `json:"archive"`
	Machine string `json:"machine"`
	Agent   string `json:"agent"`
	After   string `json:"after"`
}

func encodeHeadCursor(c headCursor) string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

func parseHeadCursor(archive, machine, after string) (headCursor, error) {
	c := headCursor{Version: 1, Archive: archive, Machine: machine, Agent: "claude"}
	if after == "" {
		return c, nil
	}
	if len(after) > 4096 {
		return c, errors.New("current-root cursor exceeds limit")
	}
	data, err := base64.RawURLEncoding.DecodeString(after)
	if err != nil {
		return c, errors.New("invalid current-root cursor")
	}
	if err = decode(data, &c); err != nil || c.Version != 1 || c.Archive != archive || c.Machine != machine || !supportedHeadAgent(c.Agent) || encodeHeadCursor(c) != after {
		return c, errors.New("invalid current-root cursor")
	}
	if c.After != "" {
		_, err = SessionIdentity(Identity{ArchiveID: archive, MachineID: machine, Agent: c.Agent}, c.After)
	}
	return c, err
}

// ReadCurrentHeads is source/spool independent. Each page performs one bounded
// namespace LIST and at most eight concurrent CURRENT reads. Empty pages can
// advance agents; callers follow next_cursor until complete then restart empty.
func ReadCurrentHeads(ctx context.Context, b Backend, archive, machine, after string, limit int) (PublicationHeads, error) {
	page := PublicationHeads{Contract: "session-publication-heads/v1", ArchiveID: archive, Heads: []PublicationHead{}}
	if limit < 1 || limit > 256 {
		return page, errors.New("current-root page must be 1..256")
	}
	c, err := parseHeadCursor(archive, machine, after)
	if err != nil {
		return page, err
	}
	scope := Identity{ArchiveID: archive, MachineID: machine, Agent: c.Agent, SessionID: "scope"}
	if _, err = SessionPrefix(scope); err != nil {
		return page, err
	}
	lister, ok := b.(SessionNamespaceLister)
	if !ok {
		return page, ErrListingUnsupported
	}
	if err := checkArchive(ctx, b, scope); err != nil {
		return page, err
	}
	prefixes, done, err := lister.ListSessionNamespaces(ctx, scope, c.After, limit)
	if err != nil {
		return page, err
	}
	if len(prefixes) > limit || (!done && len(prefixes) == 0) {
		return page, errors.New("invalid namespace page progress")
	}
	ids := make([]Identity, len(prefixes))
	previous := c.After
	for n, prefix := range prefixes {
		if prefix <= previous {
			return page, errors.New("nonadvancing namespace page")
		}
		ids[n], err = SessionIdentity(scope, prefix)
		if err != nil {
			return page, err
		}
		previous = prefix
	}
	heads, failures, err := readHeadBatch(ctx, b, ids)
	if err != nil {
		return page, err
	}
	page.Heads = heads
	page.Failures = failures
	c.After = previous
	if done {
		switch c.Agent {
		case "claude":
			c.Agent = "codex"
			c.After = ""
		case "codex":
			c.Agent = "pi"
			c.After = ""
		case "pi":
			page.Complete = true
		}
	}
	page.NextCursor = encodeHeadCursor(c)
	if err := ctx.Err(); err != nil {
		return PublicationHeads{}, err
	}
	return page, nil
}

// Each CURRENT has its own child budget. A degraded session cannot monopolize
// a sweep or suppress healthy heads; only caller cancellation and destination-wide
// authentication failures abort the page. Metadata failures expose no raw error.
func readHeadBatch(ctx context.Context, b Backend, ids []Identity) ([]PublicationHead, []PublicationHeadFailure, error) {
	results := make([]PublicationHead, len(ids))
	errs := make([]error, len(ids))
	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()
	var wg sync.WaitGroup
	for worker := range min(8, len(ids)) {
		wg.Go(func() {
			for n := worker; n < len(ids) && batchCtx.Err() == nil; n += 8 {
				currentCtx, cancel := context.WithTimeout(batchCtx, 5*time.Second)
				results[n], errs[n] = CurrentHead(currentCtx, b, ids[n])
				cancel()
				if errors.Is(errs[n], ErrDestinationPaused) {
					cancelBatch()
					return
				}
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	for _, err := range errs {
		if errors.Is(err, ErrDestinationPaused) {
			return nil, nil, err
		}
	}
	heads := make([]PublicationHead, 0, len(ids))
	var failures []PublicationHeadFailure
	for n, err := range errs {
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			failures = append(failures, PublicationHeadFailure{Identity: ids[n], Reason: "current-unreadable"})
			continue
		}
		heads = append(heads, results[n])
	}
	return heads, failures, nil
}
