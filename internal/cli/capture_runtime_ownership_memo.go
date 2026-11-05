package cli

import (
	"context"
	"sync"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

type ownerLookupFunc func(ctx context.Context, agent, root, uuid string) (session, legacy string, err error)

type ownerKey struct{ agent, root, uuid string }

type ownerResult struct {
	session, legacy string
	err             error
}

// ownershipMemo remembers the outcome of each root/UUID ownership lookup for one
// discovery call, so an owner shared by many artifacts opens the queue once.
// Errors are remembered as errors (pending, ambiguous, integrity, policy), never
// as success. Only a snapshot owner may hold one; the next call builds a new one.
type ownershipMemo struct {
	read ownerLookupFunc
	mu   sync.Mutex
	seen map[ownerKey]ownerResult
}

func newOwnershipMemo(read ownerLookupFunc) *ownershipMemo {
	return &ownershipMemo{read: read, seen: map[ownerKey]ownerResult{}}
}

func (m *ownershipMemo) lookup(ctx context.Context, agent, root, uuid string) (string, string, error) {
	k := ownerKey{agent, root, uuid}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.seen[k]; ok {
		return r.session, r.legacy, r.err
	}
	session, legacy, err := m.read(ctx, agent, root, uuid)
	// A cancelled lookup says nothing about ownership.
	if ctx.Err() == nil {
		m.seen[k] = ownerResult{session, legacy, err}
	}
	return session, legacy, err
}

// owners is the ownership reader of this composition: the live queue reader
// unless a snapshot or test replaced it.
func (r runtimeComposition) owners() ownerLookupFunc {
	if r.ownerLookup != nil {
		return r.ownerLookup
	}
	c := r.config
	return func(ctx context.Context, agent, root, uuid string) (string, string, error) {
		return sessioncapture.ReadOwnership(ctx, c.Spool, c.ArchiveID, agent, root, uuid)
	}
}

// callOptions returns opts for exactly one Discover call: one policy snapshot, one
// filesystem metadata memo (shared by Discover, the snapshot resolve and the owner
// leaf Lstat) and one ownership memo shared by the discovery LookupOwner and the privacy owner
// reached through the policy. Capture-time checks keep the live reader.
func (r runtimeComposition) callOptions(opts capturesource.Options) capturesource.Options {
	call := r
	call.ownerLookup = newOwnershipMemo(r.owners()).lookup
	call.fsMemo = capturepolicy.NewFSMemoOver(r.fsBase)
	opts.Filesystem = call.fsMemo
	owners := call.owners()
	opts.LookupOwner = func(ctx context.Context, root capturesource.Root, uuid string) (string, string, error) {
		return owners(ctx, root.Agent, root.Path, uuid)
	}
	opts.Policy = call.scopePolicy(call.snapshotPolicy(true))
	return opts
}
