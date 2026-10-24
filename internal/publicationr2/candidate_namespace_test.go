package publicationr2_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestCandidateNamespaceBindingFailsClosedWithoutGlobalMutation(t *testing.T) {
	backend := publication.NewLocal(filepath.Join(t.TempDir(), "store"))
	ctx := t.Context()
	sentinel := []byte("synthetic existing global binding sentinel")
	if err := backend.CompareAndSwap(ctx, publication.ArchiveKey, "", sentinel); err != nil {
		t.Fatal(err)
	}
	id := publication.Identity{ArchiveID: "synthetic-isolated", MachineID: "synthetic-machine", Agent: "pi", SessionID: "__archive_binding__"}
	ns := &acceptanceNamespace{Backend: backend, prefix: publication.Prefix + "/archives/" + hex.EncodeToString([]byte(id.ArchiveID)) + "/", binding: id.CurrentKey(), remaining: 30}
	expected := publication.Archive{Contract: publication.Contract, ArchiveID: id.ArchiveID}
	if err := publication.WriteArchive(ctx, ns, expected); err != nil {
		t.Fatal(err)
	}
	before, token, err := backend.Current(ctx, id.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	wrong := publication.Archive{Contract: publication.Contract, ArchiveID: "differently-initialized"}
	if err := publication.WriteArchive(ctx, ns, wrong); !errors.Is(err, publication.ErrConflict) {
		t.Fatal("different binding did not fail closed")
	}
	after, afterToken, err := backend.Current(ctx, id.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) || afterToken != token {
		t.Fatal("existing isolated binding changed")
	}
	global, _, err := backend.Current(ctx, publication.ArchiveKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(global, sentinel) {
		t.Fatal("global binding changed")
	}
}

// The adapter must preserve scope and count LISTs against the same fixed budget.
type namespaceListPeer struct {
	publication.Backend
	calls int
}

func (b *namespaceListPeer) ListSessionNamespaces(_ context.Context, scope publication.Identity, after string, limit int) ([]string, bool, error) {
	b.calls++
	if scope.Agent != "pi" || after != "" || limit != 1 {
		return nil, false, errors.New("wrong delegated list")
	}
	return nil, true, nil
}

func TestCandidateNamespaceListingFenceAndConcurrentBudget(t *testing.T) {
	id := publication.Identity{ArchiveID: "unique", MachineID: "machine", Agent: "pi", SessionID: "binding"}
	prefix := publication.Prefix + "/archives/" + hex.EncodeToString([]byte(id.ArchiveID)) + "/"
	peer := &namespaceListPeer{}
	ns := &acceptanceNamespace{Backend: peer, prefix: prefix, binding: id.CurrentKey(), remaining: 9}
	if _, done, err := ns.ListSessionNamespaces(t.Context(), id, "", 1); err != nil || !done || peer.calls != 1 {
		t.Fatal("scoped listing not delegated", err)
	}
	other := id
	other.ArchiveID = "other"
	if _, _, err := ns.ListSessionNamespaces(t.Context(), other, "", 1); err == nil || peer.calls != 1 {
		t.Fatal("outside LIST reached peer")
	}
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for range 32 {
		wg.Go(func() {
			if _, err := ns.key(id.CurrentKey()); err == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 8 || ns.remaining != 0 {
		t.Fatal("concurrent operation budget exceeded", accepted.Load(), ns.remaining)
	}
	if _, _, err := ns.ListSessionNamespaces(t.Context(), id, "", 1); err == nil || peer.calls != 1 {
		t.Fatal("exhausted LIST reached peer")
	}
	ns = &acceptanceNamespace{Backend: publication.NewLocal(t.TempDir()), prefix: prefix, remaining: 1}
	if _, _, err := ns.ListSessionNamespaces(t.Context(), id, "", 1); !errors.Is(err, publication.ErrListingUnsupported) || ns.remaining != 1 {
		t.Fatal("unsupported Local enumeration consumed budget", err)
	}
}
