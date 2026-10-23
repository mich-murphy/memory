package publication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalRetryFencesVisibleRename(t *testing.T) {
	for _, kind := range []string{"current", "archive", "immutable"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			var b *Local
			var key string
			var attempt func(*Local) error
			switch kind {
			case "current":
				local, m, _ := fixture(t)
				b, key = local, m.Identity.CurrentKey()
				attempt = func(l *Local) error {
					_, err := Publish(ctx, l, m, "")
					return err
				}
			case "archive":
				b, key = NewLocal(t.TempDir()), ArchiveKey
				attempt = func(l *Local) error {
					return WriteArchive(ctx, l, Archive{Contract: Contract, ArchiveID: "archive"})
				}
			case "immutable":
				b, key = NewLocal(t.TempDir()), "objects/immutable"
				filename := filepath.Join(t.TempDir(), "source")
				if err := os.WriteFile(filename, []byte("safe fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				attempt = func(l *Local) error { return l.PutFile(ctx, key, filename) }
			}

			dst := filepath.Join(b.root, "data", filepath.FromSlash(key))
			dir := filepath.Dir(dst)
			injected := errors.New("injected directory sync failure after rename")
			fileSyncs, dirSyncs := 0, 0
			fail := true
			hook := func(f *os.File) error {
				name := filepath.Clean(f.Name())
				if name == dst {
					fileSyncs++
				}
				if name == dir {
					// Directory creation happens before the rename; fail only
					// once the selected destination is actually visible.
					if _, err := os.Stat(dst); err == nil {
						dirSyncs++
						if fail {
							return injected
						}
					}
				}
				return f.Sync()
			}
			b.syncFile = hook
			if err := attempt(b); !errors.Is(err, ErrDurability) || !errors.Is(err, injected) {
				t.Fatalf("first attempt accepted visible unsynced bytes: %v", err)
			}
			if _, err := os.Stat(dst); err != nil {
				t.Fatalf("failure was not after rename: %v", err)
			}

			// A newly opened store has no in-memory knowledge of the failed
			// sync. Visible bytes alone still must not allow success.
			reopened := NewLocal(b.root)
			reopened.syncFile = hook
			fileSyncs, dirSyncs = 0, 0
			if err := attempt(reopened); !errors.Is(err, ErrDurability) || !errors.Is(err, injected) {
				t.Fatalf("retry bypassed failed durability fence: %v", err)
			}
			if fileSyncs == 0 || dirSyncs == 0 {
				t.Fatalf("retry did not sync file and directory: %d, %d", fileSyncs, dirSyncs)
			}
			fail = false
			fileSyncs, dirSyncs = 0, 0
			if err := attempt(reopened); err != nil {
				t.Fatalf("recovered sync: %v", err)
			}
			if fileSyncs == 0 || dirSyncs == 0 {
				t.Fatalf("successful retry did not sync file and directory: %d, %d", fileSyncs, dirSyncs)
			}
		})
	}
}
