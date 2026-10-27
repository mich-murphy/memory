package publication

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// second adds a distinct object and returns it with a manifest advancing m.
func second(t *testing.T, b *Local, m Manifest, tag string) Manifest {
	t.Helper()
	raw := []byte("payload " + tag)
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	data := enc.EncodeAll(raw, nil)
	_ = enc.Close()
	o := Object{Encoding: "zstd", SHA256: token(raw), EncodedSHA256: token(data), Size: int64(len(raw)), EncodedSize: int64(len(data))}
	o.Key = m.Identity.BaseKey() + "/objects/" + o.EncodedSHA256 + ".zst"
	p := filepath.Join(t.TempDir(), "o")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile(context.Background(), o.Key, p); err != nil {
		t.Fatal(err)
	}
	n := m
	n.PublicationID = "pub-" + tag
	n.Sequence = m.Sequence + 1
	n.Sources = append(append([]Source(nil), m.Sources...), Source{SourceID: tag, EpochID: "epoch-" + tag, Role: "transcript", Path: "/p/" + tag, State: "present", Revision: 1, Object: o})
	return n
}

func TestTrustedObjectsFallbacks(t *testing.T) {
	ctx := context.Background()
	b, m, _ := fixture(t)
	if got := TrustedObjects(ctx, b, m); got != nil {
		t.Fatal("no prior current must not trust anything")
	}
	publish(t, b, m, "")
	_, tok, _ := b.Current(ctx, m.Identity.CurrentKey())
	next := second(t, b, m, "b")
	got := TrustedObjects(ctx, b, next)
	if len(got) != 1 || !got[m.Sources[0].Object] || got[next.Sources[1].Object] {
		t.Fatalf("want only the prior object trusted, got %v", got)
	}
	// A differing size or hash for the same key is not the trusted descriptor.
	for name, mut := range map[string]func(*Object){
		"size": func(o *Object) { o.EncodedSize++ },
		"hash": func(o *Object) { o.SHA256 = token([]byte("other")) },
	} {
		changed := next
		changed.Sources = append([]Source(nil), next.Sources...)
		mut(&changed.Sources[0].Object)
		if got := TrustedObjects(ctx, b, changed); got[changed.Sources[0].Object] {
			t.Fatalf("%s change was trusted", name)
		}
	}
	// A same-sequence/older or same-id candidate is a conflict, not trusted.
	if got := TrustedObjects(ctx, b, m); got != nil {
		t.Fatal("same publication must not be trusted")
	}
	// Unreadable prior manifest: fall back to the full path.
	c, _, _ := b.Current(ctx, m.Identity.CurrentKey())
	cur, _ := parseCurrent(c, m.Identity)
	if err := os.Remove(filepath.Join(b.root, "data", filepath.FromSlash(cur.ManifestKey))); err != nil {
		t.Fatal(err)
	}
	if got := TrustedObjects(ctx, b, next); got != nil {
		t.Fatal("unreadable prior must not be trusted")
	}
	if _, err := Publish(ctx, b, next, tok); err == nil {
		t.Fatal("publish over an unreadable prior must fail closed")
	}
}

// Trust skips re-verification on publish only; Read still detects corruption
// of an old object in the closure.
func TestReadStillVerifiesTrustedObjects(t *testing.T) {
	ctx := context.Background()
	b, m, _ := fixture(t)
	publish(t, b, m, "")
	_, tok, _ := b.Current(ctx, m.Identity.CurrentKey())
	next := second(t, b, m, "b")
	publish(t, b, next, tok)
	if _, err := Read(ctx, b, m.Identity); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(b.root, "data", filepath.FromSlash(m.Sources[0].Object.Key))
	if err := os.WriteFile(old, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(ctx, b, m.Identity); err == nil {
		t.Fatal("reader must detect a corrupted old object")
	}
}
