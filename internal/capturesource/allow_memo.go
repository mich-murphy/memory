package capturesource

import (
	"context"
	"sync"
)

type allowKey struct {
	root Root
	path string
}

// allowMemo holds the policy decisions of one Discover call. The policy it wraps
// is already a per-call snapshot, so a repeated coordinate (an owner's main path
// shared by many artifacts) is evaluated once. It never outlives the call.
type allowMemo struct {
	mu   sync.Mutex
	seen map[allowKey]bool
}

func newAllowMemo() *allowMemo { return &allowMemo{seen: map[allowKey]bool{}} }

// decide returns the remembered decision or evaluates and remembers it. A decision
// reached under a cancelled context is not remembered.
func (m *allowMemo) decide(ctx context.Context, k allowKey, eval func() bool) bool {
	m.mu.Lock()
	v, ok := m.seen[k]
	m.mu.Unlock()
	if ok {
		return v
	}
	v = eval()
	if ctx.Err() == nil {
		m.mu.Lock()
		m.seen[k] = v
		m.mu.Unlock()
	}
	return v
}
