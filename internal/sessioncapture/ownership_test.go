package sessioncapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

const testOwnerUUID = "12345678-1234-1234-1234-123456789abc"

func ownerRequest(t *testing.T, root, slug string, custom bool) Request {
	t.Helper()
	uuid := testOwnerUUID
	sum := sha256.Sum256([]byte(root))
	prefix := ""
	if custom {
		prefix = "roots/" + hex.EncodeToString(sum[:])[:10] + "/"
	}
	legacy := prefix + slug + "/" + uuid
	path := filepath.Join(root, "projects", slug, uuid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, path, "malformed source is not parsed")
	return Request{Identity: publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "claude", SessionID: "session/" + hex.EncodeToString(sum[:]) + "/" + legacy}, SourceID: prefix + "projects/" + slug + "/" + uuid + ".jsonl", Role: "main", Path: path, ObservedAtNS: 1, Associations: []string{"ownership:session", "root:" + root, "adapter-session:" + legacy, "owner-uuid:" + uuid}}
}

func TestOwnershipDurableReadOnlyAndAmbiguity(t *testing.T) {
	dir, o, _ := setup(t)
	root := filepath.Join(t.TempDir(), "learned")
	a := ownerRequest(t, root, "one", true)
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close() }()
	if _, _, err = q.LookupOwnership(t.Context(), "claude", root, testOwnerUUID); !errors.Is(err, ErrOwnershipPending) {
		t.Fatal(err)
	}
	for range 2 {
		if err = q.RecordOwnership(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	session, legacy, err := ReadOwnership(t.Context(), dir, "archive", "claude", root, testOwnerUUID)
	if err != nil || session != a.Identity.SessionID || legacy != a.Associations[2][len("adapter-session:"):] {
		t.Fatalf("%q %q %v", session, legacy, err)
	}
	if _, _, err = ReadOwnership(t.Context(), dir, "wrong", "claude", root, testOwnerUUID); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	var jobs int
	if err = q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("observed != captured: %d %v", jobs, err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	// Read while closed does not require obtaining a queue flock either.
	if _, _, err = ReadOwnership(t.Context(), dir, "archive", "claude", root, testOwnerUUID); err != nil {
		t.Fatal(err)
	}
	q, err = Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	b := ownerRequest(t, root, "two", true)
	if err = q.RecordOwnership(t.Context(), b); !errors.Is(err, ErrOwnershipAmbiguous) {
		t.Fatal(err)
	}
	if err = q.RecordOwnership(t.Context(), a); !errors.Is(err, ErrOwnershipAmbiguous) {
		t.Fatal(err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ReadOwnership(t.Context(), dir, "archive", "claude", root, testOwnerUUID); !errors.Is(err, ErrOwnershipAmbiguous) {
		t.Fatal(err)
	}
	q, err = Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	var choices int
	if err = q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM source_ownership`).Scan(&choices); err != nil || choices != 2 {
		t.Fatalf("choices=%d err=%v", choices, err)
	}
}

func TestOwnershipClaimValidationAndPolicy(t *testing.T) {
	dir, o, _ := setup(t)
	root := t.TempDir()
	r := ownerRequest(t, root, "one", false)
	q := open(t, dir, o)
	mutations := []func(*Request){
		func(r *Request) { r.Identity.SessionID = "session/truncated/one/" + testOwnerUUID },
		func(r *Request) { r.SourceID = "wrong" },
		func(r *Request) { r.Path = filepath.Join(root, "elsewhere", testOwnerUUID+".jsonl") },
		func(r *Request) { r.Associations = append(r.Associations, "root:"+root) },
		func(r *Request) { r.Associations[2] = "adapter-session:other/" + testOwnerUUID },
		func(r *Request) { r.Associations = r.Associations[:2] },
		func(r *Request) { r.Identity.ArchiveID = "wrong" },
	}
	for i, mutate := range mutations {
		bad := r
		bad.Associations = append([]string(nil), r.Associations...)
		mutate(&bad)
		if err := q.RecordOwnership(t.Context(), bad); err == nil {
			t.Fatalf("mutation %d accepted", i)
		}
	}
	for _, change := range []func(*Request){func(r *Request) { r.Role = "artifact" }, func(r *Request) { r.Identity.Agent = "pi" }, func(r *Request) { r.Path = "b.jsonl" }} {
		notCandidate := r
		change(&notCandidate)
		if err := q.RecordOwnership(t.Context(), notCandidate); err != nil {
			t.Fatal("noncandidate must not block raw capture", err)
		}
	}
	if err := q.RecordOwnership(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	for _, uuid := range []string{"fakeguid", "", testOwnerUUID + "/extra"} {
		if _, _, err := q.LookupOwnership(t.Context(), "claude", root, uuid); !errors.Is(err, ErrPolicy) {
			t.Fatal(err)
		}
	}
	q.opts.CheckPolicy = func(context.Context, Request) error { return errors.New("excluded") }
	if err := q.RecordOwnership(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	for _, name := range []string{"a.jsonl", "guid.jsonl", "abc.jsonl.zst", "12345678-1234-1234-1234-123456789abc.jsonl.gz"} {
		if OwnerUUID(name) != "" {
			t.Fatal(name)
		}
	}
	if OwnerUUID(testOwnerUUID+".jsonl.zst") != testOwnerUUID {
		t.Fatal("compressed main UUID")
	}
}

func TestOwnershipCapacityCoalescesAndRootIsolation(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	root := t.TempDir()
	a := ownerRequest(t, root, "one", false)
	if err := q.RecordOwnership(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	tx, err := q.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < ownershipLimit; i++ {
		uuid := fmt.Sprintf("%08x-1234-1234-1234-123456789abc", i)
		if _, err = tx.ExecContext(t.Context(), `INSERT INTO source_ownership (agent,root,uuid,session,legacy,main_path) VALUES (?,?,?,?,?,?)`, "claude", root, uuid, "session", "legacy", uuid); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = q.RecordOwnership(t.Context(), a); err != nil {
		t.Fatal("existing coordinate at capacity", err)
	}
	b := ownerRequest(t, t.TempDir(), "one", true)
	if err = q.RecordOwnership(t.Context(), b); !errors.Is(err, ErrOwnershipCapacity) || !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	conflict := ownerRequest(t, root, "second", false)
	if err = q.RecordOwnership(t.Context(), conflict); !errors.Is(err, ErrOwnershipCapacity) {
		t.Fatal(err)
	}
	if _, _, err = q.LookupOwnership(t.Context(), "claude", root, testOwnerUUID); !errors.Is(err, ErrOwnershipAmbiguous) {
		t.Fatal("capacity conflict must invalidate old unique owner", err)
	}
	if _, _, err = ReadOwnership(t.Context(), dir, "archive", "claude", root, testOwnerUUID); !errors.Is(err, ErrOwnershipAmbiguous) {
		t.Fatal("durable ambiguity", err)
	}
	if _, _, err = q.LookupOwnership(t.Context(), "claude", filepath.Join(root, "other"), testOwnerUUID); !errors.Is(err, ErrOwnershipPending) {
		t.Fatal(err)
	}
}

func TestOwnershipReadMissingUnsafeAndCancellation(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ReadOwnership(t.Context(), dir, "archive", "claude", dir, testOwnerUUID); !errors.Is(err, ErrOwnershipPending) {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "queue.sqlite")
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadOwnership(t.Context(), dir, "archive", "claude", dir, testOwnerUUID); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	r := ownerRequest(t, t.TempDir(), "slug", false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := q.RecordOwnership(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !strings.Contains(ErrOwnershipPending.Error(), "ownership") {
		t.Fatal("safe diagnostic")
	}
}
