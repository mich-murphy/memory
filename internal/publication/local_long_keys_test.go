package publication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func longKeyFixture(t *testing.T, identity Identity) (*Local, Manifest, []byte) {
	t.Helper()
	original, m, raw := fixture(t)
	f, err := original.Open(t.Context(), m.Sources[0].Object.Key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(f)
	if err = errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
	b := NewLocal(t.TempDir())
	m.Identity = identity
	m.Sources[0].Object.Key = identity.BaseKey() + "/objects/" + m.Sources[0].Object.EncodedSHA256 + ".zst"
	if err = WriteArchive(t.Context(), b, Archive{Contract: Contract, ArchiveID: identity.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "encoded")
	if err = os.WriteFile(input, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = b.PutFile(t.Context(), m.Sources[0].Object.Key, input); err != nil {
		t.Fatal(err)
	}
	return b, m, raw
}

func TestLocalLongIdentityPublication(t *testing.T) {
	maxUTF8 := strings.Repeat("雪", 85) + "a" // 256 bytes, not 256 runes.
	for name, identity := range map[string]Identity{
		"max-utf8":             {ArchiveID: maxUTF8, MachineID: maxUTF8, Agent: maxUTF8, SessionID: maxUTF8},
		"codex-root-qualified": {ArchiveID: "archive", MachineID: "machine", Agent: "codex", SessionID: "session/" + strings.Repeat("a", 64) + "/rollout-2026-10-23T10-11-12-12345678-1234-1234-1234-123456789abc"},
		"owned-hooks-backup":   {ArchiveID: "archive", MachineID: "machine", Agent: "codex", SessionID: strings.Repeat("h", 134)},
	} {
		t.Run(name, func(t *testing.T) {
			b, m, raw := longKeyFixture(t, identity)
			publish(t, lostAck{b}, m, "")
			publish(t, NewLocal(b.root), m, "")
			got, err := Read(t.Context(), b, identity)
			if err != nil || got.Identity != identity {
				t.Fatalf("read identity: %v", err)
			}
			dest := filepath.Join(t.TempDir(), "export")
			out, err := ExportSession(t.Context(), b, identity, dest)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(dest, out.Bodies[0].Path))
			if err != nil || !bytes.Equal(body, raw) {
				t.Fatalf("body mismatch: %v", err)
			}
			_, tok, err := b.Current(t.Context(), identity.CurrentKey())
			if err != nil {
				t.Fatal(err)
			}
			next := m
			next.Sequence = 2
			next.PublicationID = "pub-2"
			publish(t, b, next, tok)
			if _, err = Publish(t.Context(), b, m, tok); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale publication: %v", err)
			}
			next.Sequence = 3
			next.PublicationID = "pub-3"
			if _, err = Publish(t.Context(), b, next, tok); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale token: %v", err)
			}
		})
	}
}

func TestLocalLongKeyMappingAndImmutable(t *testing.T) {
	for _, key := range []string{strings.Repeat("x", 241), strings.Repeat("雪", 81), strings.Repeat("a/", 400) + "end", strings.Repeat("z", 10000)} {
		t.Run(strconv.Itoa(len(key)), func(t *testing.T) {
			b := NewLocal(t.TempDir())
			input := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(input, []byte("long body"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := b.PutFile(t.Context(), key, input); err != nil {
				t.Fatal(err)
			}
			if err := NewLocal(b.root).PutFile(t.Context(), key, input); err != nil {
				t.Fatal(err)
			}
			// A short logical key resembling the reserved physical name must not alias it.
			short := "long-data/" + token([]byte(key))
			if err := b.CompareAndSwap(t.Context(), short, "", []byte("distinct short")); err != nil {
				t.Fatal(err)
			}
			for logical, want := range map[string]string{key: "long body", short: "distinct short"} {
				got, _, err := b.Current(t.Context(), logical)
				if err != nil || string(got) != want {
					t.Fatalf("logical key alias: %v", err)
				}
			}
			if err := os.WriteFile(input, []byte("different"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := b.PutFile(t.Context(), key, input); !errors.Is(err, ErrConflict) {
				t.Fatalf("immutable overwrite: %v", err)
			}
			_, tok, err := b.Current(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			if err = b.CompareAndSwap(t.Context(), key, tok, []byte("replacement")); err != nil {
				t.Fatal(err)
			}
			if err = b.CompareAndSwap(t.Context(), key, tok, []byte("stale")); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale CAS: %v", err)
			}
			if err = b.Sync(t.Context(), key); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(b.root, "long-data", token([]byte(key)))); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(b.root, "data", short)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, key := range []string{strings.Repeat("x", 240), strings.Repeat("a/", 383) + "aa"} {
		if got := localObjectName(key); got != "data/"+key {
			t.Fatal("short fixture layout changed")
		}
	}
}

func TestLocalLongKeyConfinement(t *testing.T) {
	key := strings.Repeat("x", 300)
	for _, at := range []string{"long-data", "long-data/" + token([]byte(key))} {
		t.Run(at, func(t *testing.T) {
			b := NewLocal(t.TempDir())
			if err := os.MkdirAll(filepath.Dir(filepath.Join(b.root, at)), 0o700); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(b.root, at)); err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(input, []byte("body"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, logical := range []string{key, "../" + key, key + "/../escape", "/" + key, key + "\\bad", key + "\x00"} {
				if _, err := b.Open(t.Context(), logical); err == nil {
					t.Fatal("unsafe Open")
				}
				if err := b.PutFile(t.Context(), logical, input); err == nil {
					t.Fatal("unsafe PutFile")
				}
				if err := b.CompareAndSwap(t.Context(), logical, "", []byte("body")); err == nil {
					t.Fatal("unsafe CAS")
				}
				if err := b.Sync(t.Context(), logical); err == nil {
					t.Fatal("unsafe Sync")
				}
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("escaped root: %v", err)
			}
		})
	}
}

func TestLocalLongKeyRetryFencesVisibleRename(t *testing.T) {
	for _, kind := range []string{"current", "immutable"} {
		t.Run(kind, func(t *testing.T) {
			identity := Identity{ArchiveID: "archive", MachineID: "machine", Agent: "codex", SessionID: strings.Repeat("s", 256)}
			b, m, _ := longKeyFixture(t, identity)
			key := identity.CurrentKey()
			attempt := func(l *Local) error { _, err := Publish(t.Context(), l, m, ""); return err }
			if kind == "immutable" {
				key = strings.Repeat("i", 300)
				input := filepath.Join(t.TempDir(), "input")
				if err := os.WriteFile(input, []byte("body"), 0o600); err != nil {
					t.Fatal(err)
				}
				attempt = func(l *Local) error { return l.PutFile(t.Context(), key, input) }
			}
			dst := filepath.Join(b.root, localObjectName(key))
			dir := filepath.Dir(dst)
			injected := errors.New("injected long directory sync")
			fail := true
			fileSyncs, dirSyncs := 0, 0
			hook := func(f *os.File) error {
				if filepath.Clean(f.Name()) == dst {
					fileSyncs++
				}
				if filepath.Clean(f.Name()) == dir {
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
				t.Fatalf("visible rename acknowledged: %v", err)
			}
			reopened := NewLocal(b.root)
			reopened.syncFile = hook
			fileSyncs, dirSyncs = 0, 0
			if err := attempt(reopened); !errors.Is(err, ErrDurability) {
				t.Fatalf("retry acknowledged: %v", err)
			}
			if fileSyncs == 0 || dirSyncs == 0 {
				t.Fatal("retry omitted physical fences")
			}
			fail = false
			fileSyncs, dirSyncs = 0, 0
			if err := attempt(reopened); err != nil {
				t.Fatal(err)
			}
			if fileSyncs == 0 || dirSyncs == 0 {
				t.Fatal("success omitted physical fences")
			}
		})
	}
}

func TestLocalLongKeyCASProcess(t *testing.T) {
	key := strings.Repeat("race/", 200) + "current.json"
	if root := os.Getenv("PUBLICATION_LONG_CAS_CHILD"); root != "" {
		err := NewLocal(root).CompareAndSwap(context.Background(), key, "", []byte(os.Getenv("PUBLICATION_LONG_CAS_VALUE")))
		if err == nil {
			os.Exit(0)
		}
		if errors.Is(err, ErrConflict) {
			os.Exit(3)
		}
		os.Exit(4)
	}
	root := t.TempDir()
	var wg sync.WaitGroup
	codes := make(chan int, 4)
	for n := range 4 {
		wg.Go(func() {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLocalLongKeyCASProcess$")
			cmd.Env = append(os.Environ(), "PUBLICATION_LONG_CAS_CHILD="+root, fmt.Sprintf("PUBLICATION_LONG_CAS_VALUE=%d", n))
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
