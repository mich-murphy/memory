package store_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/store"
)

// exercise runs the same checks against any Store.
func exercise(t *testing.T, s store.Store, prefix string) {
	t.Helper()
	ctx := context.Background()
	k := func(name string) string { return prefix + name }
	if err := s.Put(ctx, k("a/one"), []byte("1"), map[string]string{"sha256": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, k("a/two"), []byte("22"), map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, k("b/three"), []byte("333"), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, k("a/two")); err != nil || string(got) != "22" {
		t.Errorf("Get = %q, %v", got, err)
	}
	if _, err := s.Get(ctx, k("missing")); err == nil {
		t.Error("Get of a missing key succeeded")
	}
	keys, err := s.List(ctx, k("a/"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{k("a/one"), k("a/two")}) {
		t.Errorf("List = %v", keys)
	}
	if ok, err := s.Exists(ctx, k("a/one")); !ok || err != nil {
		t.Errorf("Exists = %v, %v", ok, err)
	}
	if ok, err := s.Exists(ctx, k("nope")); ok || err != nil {
		t.Errorf("Exists(missing) = %v, %v", ok, err)
	}
	if ok, err := s.Copy(ctx, k("a/one"), k("c/copy")); !ok || err != nil {
		t.Errorf("Copy = %v, %v", ok, err)
	}
	if got, _ := s.Get(ctx, k("c/copy")); string(got) != "1" {
		t.Errorf("copy holds %q", got)
	}
	if ok, err := s.Copy(ctx, k("nope"), k("c/none")); ok || err != nil {
		t.Errorf("Copy(missing) = %v, %v", ok, err)
	}

	big := make([]byte, 11<<20+123)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFile(ctx, k("big"), path, map[string]string{"role": "main"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, k("big")); err != nil || !bytes.Equal(got, big) {
		t.Errorf("multipart round trip: %d bytes, %v", len(got), err)
	}
	small := filepath.Join(t.TempDir(), "small")
	if err := os.WriteFile(small, []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFile(ctx, k("small"), small, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFile(ctx, k("x"), filepath.Join(t.TempDir(), "absent"), nil); err == nil {
		t.Error("PutFile of a missing file succeeded")
	}

	for _, key := range []string{"a/one", "a/two", "b/three", "c/copy", "big", "small"} {
		if err := s.Delete(ctx, k(key)); err != nil {
			t.Errorf("Delete %s: %v", key, err)
		}
	}
	if err := s.Delete(ctx, k("never-there")); err != nil {
		t.Errorf("Delete(missing) = %v", err)
	}
	if keys, err := s.List(ctx, prefix); err != nil || len(keys) != 0 {
		t.Errorf("left behind: %v, %v", keys, err)
	}
}

func TestLocal(t *testing.T) {
	exercise(t, store.NewLocal(t.TempDir()), "")
}

func TestR2AgainstAFakeS3(t *testing.T) {
	store.SetMultipart(t, 1<<20, 5<<20)
	backend := s3mem.New()
	if err := backend.CreateBucket("sessions"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	s, err := store.NewR2(&config.Config{
		R2Endpoint: srv.URL, R2Bucket: "sessions", R2AccessKeyID: "id", R2SecretAccessKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, s, "")
}

func TestOpenNeedsR2SettingsUnlessLocal(t *testing.T) {
	if _, err := store.Open(&config.Config{}); err == nil {
		t.Error("opened R2 without settings")
	}
	if s, err := store.Open(&config.Config{LocalStore: t.TempDir()}); err != nil || s == nil {
		t.Errorf("Open(local) = %v, %v", s, err)
	}
}

// TestR2Live runs against the real bucket in R2_* when AGENT_SESSIONS_TEST_R2=1, under a
// throwaway prefix it removes again.
func TestR2Live(t *testing.T) {
	if os.Getenv("AGENT_SESSIONS_TEST_R2") != "1" {
		t.Skip("set AGENT_SESSIONS_TEST_R2=1 and R2_* to run against a real bucket")
	}
	store.SetMultipart(t, 1<<20, 5<<20)
	s, err := store.NewR2(&config.Config{
		R2Endpoint: os.Getenv("R2_ENDPOINT"), R2Bucket: os.Getenv("R2_BUCKET"),
		R2AccessKeyID: os.Getenv("R2_ACCESS_KEY_ID"), R2SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, s, "_agent-sessions/test/"+rand.Text()+"/")
}
