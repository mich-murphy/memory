package sessioncapture

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// opCountBackend records remote object work for scaling measurements.
type opCountBackend struct {
	publication.Backend
	putObjects, putMeta, openObjects, openMeta, current atomic.Int64
	objectBytesRead                                     atomic.Int64
}

func (c *opCountBackend) reset() {
	for _, v := range []*atomic.Int64{&c.putObjects, &c.putMeta, &c.openObjects, &c.openMeta, &c.current, &c.objectBytesRead} {
		v.Store(0)
	}
}

func (c *opCountBackend) Sync(ctx context.Context, key string) error {
	if d, ok := c.Backend.(publication.DurabilityFence); ok {
		return d.Sync(ctx, key)
	}
	return nil
}

func (c *opCountBackend) PutFile(ctx context.Context, key, path string) error {
	if strings.HasSuffix(key, ".zst") {
		c.putObjects.Add(1)
	} else {
		c.putMeta.Add(1)
	}
	return c.Backend.PutFile(ctx, key, path)
}

func (c *opCountBackend) Current(ctx context.Context, key string) ([]byte, string, error) {
	c.current.Add(1)
	return c.Backend.Current(ctx, key)
}

type countedReader struct {
	io.ReadCloser
	n *atomic.Int64
}

func (r countedReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.n.Add(int64(n))
	return n, err
}

func (c *opCountBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	f, err := c.Backend.Open(ctx, key)
	if err != nil {
		return f, err
	}
	if strings.HasSuffix(key, ".zst") {
		c.openObjects.Add(1)
		return countedReader{f, &c.objectBytesRead}, nil
	}
	c.openMeta.Add(1)
	return f, nil
}

// Per-publication remote object work must track the delta (one new source),
// not the session's accumulated object set.
func TestDeliveryCostIsProportionalToDelta(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	cb := &opCountBackend{Backend: newBackend(t)}
	root := filepath.Dir(r.Path)
	const n = 12
	for k := 1; k <= n; k++ {
		req := r
		req.SourceID = fmt.Sprintf("source-%d", k)
		req.Path = filepath.Join(root, fmt.Sprintf("s%d.jsonl", k))
		write(t, req.Path, strings.Repeat(fmt.Sprintf("line %d payload\n", k), 2000))
		capture(t, q, req)
		cb.reset()
		if e := q.Deliver(t.Context(), cb, r.Identity); e != nil {
			t.Fatal(e)
		}
		t.Logf("pub %2d: putObj=%d putMeta=%d openObj=%d openMeta=%d current=%d objBytesRead=%d",
			k, cb.putObjects.Load(), cb.putMeta.Load(), cb.openObjects.Load(), cb.openMeta.Load(), cb.current.Load(), cb.objectBytesRead.Load())
		if k > 1 && (cb.putObjects.Load() > 1 || cb.openObjects.Load() > 1) {
			t.Fatalf("publication %d handled %d put / %d open object operations; want O(delta)", k, cb.putObjects.Load(), cb.openObjects.Load())
		}
	}
	m, err := publication.Read(t.Context(), cb, r.Identity)
	if err != nil || len(m.Sources) != n {
		t.Fatal(err, len(m.Sources))
	}
}
