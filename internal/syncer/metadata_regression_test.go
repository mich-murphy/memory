package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

type metadataListStore struct {
	store.Store
	list  func(string) (store.Page, error)
	reads int
	heads int
}

func (b *metadataListStore) List(_ context.Context, _, token string, _ int) (store.Page, error) {
	return b.list(token)
}

func (b *metadataListStore) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	b.reads++
	return b.Store.OpenReader(ctx, key)
}

func (b *metadataListStore) Head(ctx context.Context, key string) (store.Object, error) {
	b.heads++
	return b.Store.Head(ctx, key)
}

func metadataFixture(t *testing.T) (*Syncer, *metadataListStore) {
	t.Helper()
	cfg := &config.Config{StateDir: t.TempDir(), LocalStore: t.TempDir(), MachineID: "machine"}
	db, err := state.Open(t.Context(), cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	bucket := &metadataListStore{Store: store.NewLocal(cfg.LocalStore)}
	return New(cfg, bucket, db, nil, Options{}), bucket
}

func TestMetadataEnumerationBatchBoundary(t *testing.T) {
	for _, mode := range []string{"complete", "listError", "missing", "sizeError"} {
		t.Run(mode, func(t *testing.T) {
			sy, bucket := metadataFixture(t)
			const prefix = "v3/records/metadata/"
			objects := make([]store.Object, 1001)
			if err := sy.db.Atomic(func() error {
				for i := range objects {
					key := fmt.Sprintf("%s%04d.json", prefix, i)
					objects[i] = store.Object{Key: key, Size: 4}
					if err := sy.db.RecordArchive(state.Record{Key: key, Body: []byte("body")}); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 1000)
			for i := range keys {
				keys[i] = objects[i].Key
			}
			if err := sy.db.MarkArchiveSeenBatch(keys, "old"); err != nil {
				t.Fatal(err)
			}
			if err := sy.db.MarkArchiveSeen(objects[1000].Key, "old"); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected LIST error")
			bucket.list = func(token string) (store.Page, error) {
				// If enumeration held a transaction across LIST this independent domain fails.
				if err := sy.db.SetCheckpoint("network-probe", false); err != nil {
					t.Fatal(err)
				}
				if token == "" {
					return store.Page{Objects: objects[:1000], Next: "next"}, nil
				}
				// Exactly 1000 validated keys must already be committed at the boundary.
				for _, i := range []int{0, 999} {
					missing, err := sy.db.MissingArchive(objects[i].Key, "old")
					if err != nil || missing != objects[i].Key {
						t.Fatalf("boundary marker %d not flushed: %s, %v", i, missing, err)
					}
				}
				switch mode {
				case "listError":
					return store.Page{}, injected
				case "missing":
					return store.Page{}, nil
				case "sizeError":
					bad := objects[1000]
					bad.Size++
					return store.Page{Objects: []store.Object{bad}}, nil
				default:
					return store.Page{Objects: objects[1000:]}, nil
				}
			}
			err := sy.enumerateRecords(t.Context(), prefix, false)
			switch mode {
			case "complete":
				if err != nil {
					t.Fatal(err)
				}
			case "listError":
				if !errors.Is(err, injected) {
					t.Fatalf("error = %v", err)
				}
			case "missing":
				if err == nil || !strings.Contains(err.Error(), "previously recorded immutable object is missing") {
					t.Fatalf("error = %v", err)
				}
			case "sizeError":
				if err == nil || !strings.Contains(err.Error(), "immutable record size changed") {
					t.Fatalf("error = %v", err)
				}
			}
			if bucket.reads != 0 {
				t.Fatalf("warm metadata scan fetched %d bodies", bucket.reads)
			}
			missing, e := sy.db.MissingArchive(objects[1000].Key, "old")
			if e != nil {
				t.Fatal(e)
			}
			if (missing != "") != (mode == "complete") {
				t.Fatalf("final marker changed after %s: %q", mode, missing)
			}
		})
	}
}

func TestMetadataColdImportStillVerifiesImmutableBytes(t *testing.T) {
	sy, bucket := metadataFixture(t)
	// The existing remote checksum verification must still run on cold imports,
	// even if cached metadata says the length is unchanged.
	key := "v3/records/metadata/corrupt.json"
	body := []byte("not a publication")
	if err := sy.db.ImportPlan(state.Record{Key: key, Body: body}); err != nil {
		t.Fatal(err)
	}
	if err := bucket.Put(t.Context(), key, body, nil); err != nil {
		t.Fatal(err)
	}
	object := store.Object{Key: key, Size: int64(len(body))}
	if err := sy.importListedObject(t.Context(), object, false); err != nil {
		t.Fatal(err)
	}
	if bucket.reads != 0 {
		t.Fatal("warm path fetched body")
	}
	if err := sy.importListedObject(t.Context(), object, true); err == nil {
		t.Fatal("cold path accepted invalid evidence")
	}
	if bucket.heads == 0 {
		t.Fatal("cold path skipped remote verification")
	}
}

func TestMetadataReplayAppliedStillDecodesAndProjectsPending(t *testing.T) {
	sy, _ := metadataFixture(t)
	p := branchPublication("root")
	body, id, err := canonicalPublication(p)
	if err != nil {
		t.Fatal(err)
	}
	r := recordFor("root-commit", id, p, body, true)
	if err := sy.db.ImportPlan(r); err != nil {
		t.Fatal(err)
	}
	if err := sy.replayStream(r.Kind, r.Stream); err != nil {
		t.Fatal(err)
	}
	applied, err := sy.db.ArchiveApplied(r.Key)
	if err != nil || !applied {
		t.Fatalf("applied = %v, %v", applied, err)
	}
	roots, err := sy.db.ExtraRoots(p.Agent)
	if err != nil || len(roots) != 1 || roots[0] != p.Root.Path {
		t.Fatalf("roots = %v, %v", roots, err)
	}
	if err := sy.replayStream(r.Kind, r.Stream); err != nil {
		t.Fatal(err)
	}
	// An applied marker cannot skip decoding committed evidence.
	bad := state.Record{Key: "bad", Kind: "root", Stream: "bad", ID: "bad", Sequence: 1, Committed: true, Body: []byte("invalid")}
	if err := sy.db.Atomic(func() error {
		if err := sy.db.RecordArchive(bad); err != nil {
			return err
		}
		return sy.db.MarkArchiveApplied(bad.Key)
	}); err != nil {
		t.Fatal(err)
	}
	if err := sy.replayStream(bad.Kind, bad.Stream); err == nil {
		t.Fatal("applied flag bypassed descriptor decoding")
	}
}

func TestMetadataPageValidation(t *testing.T) {
	for _, mode := range []string{"duplicate", "invalidPrefix", "invalidKey", "repeatedToken", "cancelled", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			sy, bucket := metadataFixture(t)
			const prefix = "v3/records/metadata/"
			object := store.Object{Key: prefix + "empty.json", Size: 0}
			if err := sy.db.ImportPlan(state.Record{Key: object.Key, Body: []byte{}}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			bucket.list = func(token string) (store.Page, error) {
				calls++
				switch mode {
				case "invalidPrefix":
					return store.Page{Objects: []store.Object{{Key: "elsewhere"}}}, nil
				case "invalidKey":
					return store.Page{Objects: []store.Object{{Key: prefix + "../bad"}}}, nil
				case "repeatedToken":
					return store.Page{Objects: []store.Object{object}, Next: "same"}, nil
				case "oversized":
					objects := make([]store.Object, 1001)
					for i := range objects {
						objects[i] = object
					}
					return store.Page{Objects: objects}, nil
				default:
					if token == "" {
						return store.Page{Objects: []store.Object{object, object}, Next: "next"}, nil
					}
					return store.Page{Objects: []store.Object{object}}, nil
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			err := sy.enumerateRecords(ctx, prefix, false)
			if mode == "duplicate" {
				if err != nil || calls != 2 {
					t.Fatalf("duplicate enumeration = %v, %d calls", err, calls)
				}
			} else if err == nil {
				t.Fatal("invalid page accepted")
			}
			if mode == "cancelled" && (calls != 0 || !errors.Is(err, context.Canceled)) {
				t.Fatalf("cancelled LIST = %v, %d calls", err, calls)
			}
			if mode == "repeatedToken" && calls != 2 {
				t.Fatalf("repeated token took %d calls", calls)
			}
			if bucket.heads != 0 || bucket.reads != 0 {
				t.Fatal("cached page performed remote verification")
			}
		})
	}
}
