package cli

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/publicationr2"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// Runtime remote availability is delivery state, never a source-capture startup
// prerequisite. Client construction and archive binding are retried by bounded
// delivery workers. No failed initialization is cached.
type captureDestinationBackend struct {
	mu           sync.Mutex
	backend      publication.Backend
	spec         capturebackend.Spec
	archive      string
	bound        bool
	dependencies capturebackend.Dependencies
}

// captureRuntimeBackend binds Local before queue startup; remote binding remains
// lazy inside delivery so credentials and network availability cannot gate capture.
func captureRuntimeBackend(ctx context.Context, c capturedaemon.Config) (publication.Backend, error) {
	b, err := captureBackend(ctx, c, true)
	if err != nil || c.BackendSpec().Kind != "local" {
		return b, err
	}
	a, err := publication.ReadArchive(ctx, b)
	if err != nil {
		return nil, err
	}
	if a.ArchiveID != c.ArchiveID {
		return nil, errors.New("capture archive identity mismatch")
	}
	return b, nil
}

func captureBackend(ctx context.Context, c capturedaemon.Config, lazy bool) (publication.Backend, error) {
	b := &captureDestinationBackend{spec: c.BackendSpec(), archive: c.ArchiveID}
	if lazy && b.spec.Kind == "r2" {
		return b, nil
	}
	var err error
	b.backend, err = capturebackend.Open(ctx, b.spec, b.dependencies)
	if err != nil {
		return nil, destinationError(err)
	}
	b.bound = true // explicit init/export validate their own archive through the reader
	return b, nil
}

func destinationError(err error) error {
	if errors.Is(err, publicationr2.ErrDestinationPaused) {
		return errors.Join(sessioncapture.ErrDestination, err)
	}
	return err
}

func (b *captureDestinationBackend) get(ctx context.Context, key string) (publication.Backend, error) {
	b.mu.Lock()
	backend, bound := b.backend, b.bound
	b.mu.Unlock()
	if backend == nil {
		var err error
		backend, err = capturebackend.Open(ctx, b.spec, b.dependencies)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			return nil, errors.Join(sessioncapture.ErrDestination, err)
		}
		b.mu.Lock()
		if b.backend == nil {
			b.backend = backend
		} else {
			backend = b.backend
		}
		b.mu.Unlock()
	}
	if !bound && key != publication.ArchiveKey {
		archive, err := publication.ReadArchive(ctx, backend)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, publication.ErrDurability) {
				return nil, err
			}
			return nil, errors.Join(sessioncapture.ErrDestination, destinationError(err))
		}
		if archive.ArchiveID != b.archive {
			return nil, errors.Join(sessioncapture.ErrDestination, sessioncapture.ErrIntegrity, errors.New("runtime archive identity mismatch"))
		}
		b.mu.Lock()
		b.bound = true
		b.mu.Unlock()
	}
	return backend, nil
}

func (b *captureDestinationBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	backend, err := b.get(ctx, key)
	if err != nil {
		return nil, err
	}
	r, err := backend.Open(ctx, key)
	return r, destinationError(err)
}

func (b *captureDestinationBackend) PutFile(ctx context.Context, key, path string) error {
	backend, err := b.get(ctx, key)
	if err != nil {
		return err
	}
	return destinationError(backend.PutFile(ctx, key, path))
}

func (b *captureDestinationBackend) Current(ctx context.Context, key string) ([]byte, string, error) {
	backend, err := b.get(ctx, key)
	if err != nil {
		return nil, "", err
	}
	body, token, err := backend.Current(ctx, key)
	return body, token, destinationError(err)
}

func (b *captureDestinationBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	backend, err := b.get(ctx, key)
	if err != nil {
		return err
	}
	return destinationError(backend.CompareAndSwap(ctx, key, token, body))
}

func (b *captureDestinationBackend) Sync(ctx context.Context, key string) error {
	backend, err := b.get(ctx, key)
	if err != nil {
		return err
	}
	if fence, ok := backend.(publication.DurabilityFence); ok {
		return destinationError(fence.Sync(ctx, key))
	}
	return nil
}

func (b *captureDestinationBackend) ListSessionNamespaces(ctx context.Context, scope publication.Identity, after string, limit int) ([]string, bool, error) {
	backend, err := b.get(ctx, publication.ArchiveKey)
	if err != nil {
		return nil, false, err
	}
	lister, ok := backend.(publication.SessionNamespaceLister)
	if !ok {
		return nil, false, publication.ErrListingUnsupported
	}
	prefixes, complete, err := lister.ListSessionNamespaces(ctx, scope, after, limit)
	return prefixes, complete, destinationError(err)
}
