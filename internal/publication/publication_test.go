package publication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func fixture(t *testing.T) (*Local, Manifest, []byte) {
	t.Helper()
	ctx := context.Background()
	b := NewLocal(t.TempDir())
	i := Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: "session/../雪"}
	if err := WriteArchive(ctx, b, Archive{Contract: Contract, ArchiveID: i.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	raw := append([]byte("{\"role\":\"user\"}\n"), []byte{0, 255, 13, 10}...)
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	data := enc.EncodeAll(raw, nil)
	if err = enc.Close(); err != nil {
		t.Fatal(err)
	}
	o := Object{Encoding: "zstd", SHA256: token(raw), EncodedSHA256: token(data), Size: int64(len(raw)), EncodedSize: int64(len(data))}
	o.Key = i.BaseKey() + "/objects/" + o.EncodedSHA256 + ".zst"
	p := filepath.Join(t.TempDir(), "object")
	if err = os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = b.PutFile(ctx, o.Key, p); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Contract: Contract, Identity: i, PublicationID: "pub-1", Sequence: 1, Coverage: Coverage{Discovery: "complete", Enrichment: "partial", Missing: []string{"launch"}}, Sources: []Source{{SourceID: "source", EpochID: "epoch", Role: "transcript", Path: "/original/session", State: "present", Revision: 1, Object: o}}}
	return b, m, raw
}

func publish(t *testing.T, b Backend, m Manifest, expected string) Current {
	t.Helper()
	c, err := Publish(context.Background(), b, m, expected)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestExactPinnedExport(t *testing.T) {
	b, m, raw := fixture(t)
	publish(t, b, m, "")
	got, err := Read(context.Background(), b, m.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity != m.Identity || got.Sources[0].Object != m.Sources[0].Object {
		t.Fatal("manifest mismatch")
	}
	// Updating current as the first dependency is opened must not mix publications.
	wrapper := &advanceOnOpen{Backend: b, t: t, next: m}
	wrapper.next.Sequence = 2
	wrapper.next.PublicationID = "pub-2"
	dest := filepath.Join(t.TempDir(), "export")
	out, err := ExportSession(context.Background(), wrapper, m.Identity, dest)
	if err != nil {
		t.Fatal(err)
	}
	if out.Contract != ReaderContract || out.Manifest.PublicationID != m.PublicationID {
		t.Fatal("export not pinned")
	}
	data, err := os.ReadFile(filepath.Join(dest, out.Bodies[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, raw) {
		t.Fatal("body bytes differ")
	}
	metadata, err := os.ReadFile(filepath.Join(dest, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed Export
	if err = json.Unmarshal(metadata, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Manifest.PublicationID != out.Manifest.PublicationID || filepath.IsAbs(parsed.Bodies[0].Path) {
		t.Fatal("bad export metadata")
	}
	st, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatal("export not private")
	}
	if _, err = ExportSession(context.Background(), b, m.Identity, dest); err == nil {
		t.Fatal("existing export accepted")
	}
}

type advanceOnOpen struct {
	Backend
	t    *testing.T
	next Manifest
	once sync.Once
}

func (w *advanceOnOpen) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == w.next.Sources[0].Object.Key {
		w.once.Do(func() {
			_, tok, err := w.Current(ctx, w.next.Identity.CurrentKey())
			if err != nil {
				w.t.Fatal(err)
			}
			if _, err = Publish(ctx, w.Backend, w.next, tok); err != nil {
				w.t.Fatal(err)
			}
		})
	}
	return w.Backend.Open(ctx, key)
}

type lostAck struct{ Backend }

func (l lostAck) Sync(ctx context.Context, key string) error {
	return fence(ctx, l.Backend, key)
}

func (l lostAck) CompareAndSwap(ctx context.Context, k, tok string, v []byte) error {
	if err := l.Backend.CompareAndSwap(ctx, k, tok, v); err != nil {
		return err
	}
	return errors.New("lost ack")
}

func TestLostAckStaleAndArchiveBinding(t *testing.T) {
	b, m, _ := fixture(t)
	ctx := context.Background()
	c := publish(t, lostAck{b}, m, "")
	if c.Sequence != 1 {
		t.Fatal("bad sequence")
	}
	publish(t, b, m, "") // stable retry ignores the obsolete caller token
	_, tok, err := b.Current(ctx, m.Identity.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	next := m
	next.Sequence = 2
	next.PublicationID = "pub-2"
	publish(t, b, next, tok)
	if _, err = Publish(ctx, b, m, tok); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	next.Sequence = 3
	next.PublicationID = "pub-3"
	if _, err = Publish(ctx, b, next, tok); !errors.Is(err, ErrConflict) {
		t.Fatalf("old token: %v", err)
	}
	if err = WriteArchive(ctx, b, Archive{Contract: Contract, ArchiveID: "different"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("archive replacement: %v", err)
	}
	wrong := m.Identity
	wrong.ArchiveID = "different"
	if _, err = Read(ctx, b, wrong); err == nil {
		t.Fatal("wrong archive accepted")
	}
	if err = WriteArchive(ctx, lostAck{b}, Archive{Contract: Contract, ArchiveID: m.Identity.ArchiveID}); err != nil {
		t.Fatal(err)
	}
}

func TestMissingCorruptAndUnsupported(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "decoded-size", "encoded-size", "hash", "contract", "encoding", "source-limit"} {
		t.Run(mode, func(t *testing.T) {
			b, m, _ := fixture(t)
			ctx := context.Background()
			switch mode {
			case "missing":
				if err := os.Remove(filepath.Join(b.root, "data", filepath.FromSlash(m.Sources[0].Object.Key))); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(b.root, "data", filepath.FromSlash(m.Sources[0].Object.Key)), []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "decoded-size":
				m.Sources[0].Object.Size--
			case "encoded-size":
				m.Sources[0].Object.EncodedSize--
			case "hash":
				m.Sources[0].Object.SHA256 = token([]byte("wrong"))
			case "contract":
				m.Contract = "future"
			case "encoding":
				m.Sources[0].Object.Encoding = "gzip"
			case "source-limit":
				for len(m.Sources) <= MaxSources {
					m.Sources = append(m.Sources, m.Sources[0])
				}
			}
			if _, err := Publish(ctx, b, m, ""); err == nil {
				t.Fatal("invalid dependency accepted")
			}
			if _, _, err := b.Current(ctx, m.Identity.CurrentKey()); !errors.Is(err, ErrNotFound) {
				t.Fatal("failed publication changed current")
			}
		})
	}
	b, m, _ := fixture(t)
	publish(t, b, m, "")
	ctx := context.Background()
	p := filepath.Join(b.root, "data", filepath.FromSlash(m.Sources[0].Object.Key))
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(ctx, b, m.Identity); err == nil {
		t.Fatal("missing read accepted")
	}
	dest := filepath.Join(t.TempDir(), "failed")
	if _, err := ExportSession(ctx, b, m.Identity, dest); err == nil {
		t.Fatal("missing export accepted")
	}
	if _, err := os.Stat(filepath.Join(dest, "metadata.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed export marked complete")
	}
}

func TestUnsupportedRootsAndMetadataBounds(t *testing.T) {
	b, m, _ := fixture(t)
	ctx := context.Background()
	data := []byte(`{"contract":"future","archive_id":"archive"}`)
	_, tok, err := b.Current(ctx, ArchiveKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.CompareAndSwap(ctx, ArchiveKey, tok, data); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadArchive(ctx, b); err == nil {
		t.Fatal("unknown archive accepted")
	}
	if _, err = Read(ctx, b, m.Identity); err == nil {
		t.Fatal("unknown archive read")
	}
	if err = b.CompareAndSwap(ctx, "huge", "", make([]byte, MaxMetadata+1)); err == nil {
		t.Fatal("large metadata accepted")
	}
}

func TestImmutableAndConfinement(t *testing.T) {
	b, m, _ := fixture(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(p, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile(ctx, m.Sources[0].Object.Key, p); !errors.Is(err, ErrConflict) {
		t.Fatalf("immutable replacement: %v", err)
	}
	for _, key := range []string{"../escape", "/absolute", "a/../../x", "a//b", "a\\b", ".", ""} {
		if err := b.PutFile(ctx, key, p); err == nil {
			t.Fatalf("unsafe key accepted: %q", key)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(b.root, "data", "link")); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile(ctx, "link/escape", p); err == nil {
		t.Fatal("symlink write")
	}
	if _, err := b.Open(ctx, "link/escape"); err == nil {
		t.Fatal("symlink read")
	}
}

// Each child independently opens the same backend. An exclusive filesystem flock
// must serialize the read/check/replace across processes, not just goroutines.
func TestCASProcess(t *testing.T) {
	if root := os.Getenv("PUBLICATION_CAS_CHILD"); root != "" {
		b := NewLocal(root)
		err := b.CompareAndSwap(context.Background(), "race/current.json", "", []byte(os.Getenv("PUBLICATION_CAS_VALUE")))
		if err == nil {
			os.Exit(0)
		}
		if errors.Is(err, ErrConflict) {
			os.Exit(3)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	root := t.TempDir()
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for n := range 8 {
		wg.Go(func() {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCASProcess$")
			cmd.Env = append(os.Environ(), "PUBLICATION_CAS_CHILD="+root, fmt.Sprintf("PUBLICATION_CAS_VALUE=%d", n))
			err := cmd.Run()
			if err == nil {
				codes <- 0
				return
			}
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				codes <- exit.ExitCode()
			} else {
				codes <- 99
			}
		})
	}
	wg.Wait()
	close(codes)
	wins := 0
	for code := range codes {
		if code == 0 {
			wins++
		} else if code != 3 {
			t.Fatalf("child exit %d", code)
		}
	}
	if wins != 1 {
		t.Fatalf("CAS winners=%d", wins)
	}
}

func TestRetainedEpochAndMissingManifestRetry(t *testing.T) {
	b, m, _ := fixture(t)
	ctx := context.Background()
	c := publish(t, b, m, "")
	_, tok, err := b.Current(ctx, m.Identity.CurrentKey())
	if err != nil {
		t.Fatal(err)
	}
	next := m
	next.Sequence = 2
	next.PublicationID = "pub-2"
	next.Sources = nil
	if _, err = Publish(ctx, b, next, tok); !errors.Is(err, ErrConflict) {
		t.Fatalf("epoch removal: %v", err)
	}
	if err = os.Remove(filepath.Join(b.root, "data", filepath.FromSlash(c.ManifestKey))); err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(ctx, b, m, ""); err == nil {
		t.Fatal("retry accepted missing selected manifest")
	}
}
