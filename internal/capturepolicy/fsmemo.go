package capturepolicy

import (
	"os"
	"sync"
)

// Filesystem is the metadata view Resolve walks: Lstat and Readlink only.
type Filesystem interface {
	Lstat(string) (os.FileInfo, error)
	Readlink(string) (string, error)
}

type liveFS struct{}

func (liveFS) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }

func (liveFS) Readlink(path string) (string, error) { return os.Readlink(path) }

type lstatResult struct {
	info os.FileInfo
	err  error
}

type linkResult struct {
	target string
	err    error
}

// FSMemo looks up each path's Lstat and Readlink at most once, errors included. It is a
// snapshot of one discovery call: it never observes later changes, so its owner must
// drop it when the call ends and take a fresh one next time. Never hold it across calls
// or use it for the live recheck before capture or delivery (use Resolve there).
type FSMemo struct {
	base   Filesystem
	mu     sync.Mutex
	lstats map[string]lstatResult
	links  map[string]linkResult
}

// NewFSMemo returns an empty memo over the live filesystem.
func NewFSMemo() *FSMemo { return NewFSMemoOver(nil) }

// NewFSMemoOver returns an empty memo over base; nil selects the live filesystem.
func NewFSMemoOver(base Filesystem) *FSMemo {
	if base == nil {
		base = liveFS{}
	}
	return &FSMemo{base: base, lstats: map[string]lstatResult{}, links: map[string]linkResult{}}
}

// Lstat implements Filesystem.
func (m *FSMemo) Lstat(path string) (os.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.lstats[path]
	if !ok {
		r.info, r.err = m.base.Lstat(path)
		m.lstats[path] = r
	}
	return r.info, r.err
}

// Readlink implements Filesystem.
func (m *FSMemo) Readlink(path string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.links[path]
	if !ok {
		r.target, r.err = m.base.Readlink(path)
		m.links[path] = r
	}
	return r.target, r.err
}

// Resolve is ResolveWith over the memo.
func (m *FSMemo) Resolve(path string) (Resolution, error) { return ResolveWith(path, m) }
