package sessioncapture

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/publication"
)

func sqliteQueueDB(t *testing.T, path string, wal bool, marker string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if wal {
		if _, err := db.ExecContext(t.Context(), "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE evidence(body TEXT); INSERT INTO evidence VALUES (?), (?), (?)", marker, marker, marker); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDerivedSQLiteAuthorizedCallbackSwap(t *testing.T) {
	for _, wal := range []bool{false, true} {
		for _, leaf := range []bool{false, true} {
			for _, early := range []bool{false, true} {
				if early && wal && leaf {
					continue
				} // WAL authority is first observed at adapter entry, not initial DB authorization.
				name := map[bool]string{false: "db", true: "wal"}[wal] + map[bool]string{false: "/parent", true: "/leaf"}[leaf] + map[bool]string{false: "/final", true: "/initial"}[early]
				t.Run(name, func(t *testing.T) {
					dir, o, r := setup(t)
					parent, outside := filepath.Join(filepath.Dir(r.Path), "allowed"), filepath.Join(filepath.Dir(r.Path), "outside")
					for _, p := range []string{parent, outside} {
						if err := os.Mkdir(p, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					r.Path = filepath.Join(parent, "session.sqlite")
					r.Transform = "sqlite-backup"
					sqliteQueueDB(t, r.Path, true, "safe")
					forbidden := filepath.Join(outside, "session.sqlite")
					sqliteQueueDB(t, forbidden, true, "FORBIDDEN_CALLBACK_BYTES")
					calls, target := 0, 0
					swapped := false
					p, to := parent, outside
					if leaf {
						p, to = r.Path, forbidden
						if wal {
							p += "-wal"
							to += "-wal"
						}
					}
					restore := func() {
						if !swapped {
							return
						}
						if err := os.Remove(p); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(p+"-original", p); err != nil {
							t.Fatal(err)
						}
						swapped = false
					}
					defer restore()
					o.CheckPolicy = func(context.Context, Request) error {
						calls++
						if target > 0 && calls == target {
							if err := os.Rename(p, p+"-original"); err != nil {
								t.Fatal(err)
							}
							if err := os.Symlink(to, p); err != nil {
								t.Fatal(err)
							}
							swapped = true
						}
						return nil
					}
					o.Fault = func(point string) error {
						if point == "payload-before-sync" {
							restore()
						}
						return nil
					}
					q := open(t, dir, o)
					// A complete harmless capture measures the final policy callback before
					// the payload boundary. Failures must occur before attacker bytes persist.
					atPayload := 0
					q.opts.Fault = func(point string) error {
						if point == "payload-before-sync" {
							atPayload = calls
						}
						return nil
					}
					capture(t, q, r)
					final := atPayload
					calls = 0
					if _, err := q.check(t.Context(), evidence{Request: r}, false); err != nil {
						t.Fatal(err)
					}
					initial := calls
					// Use a fresh session so latestPolicyChecked adds no unrelated callbacks.
					r.Identity.SessionID = "attack"
					calls = 0
					target = final
					if early {
						target = initial
					}
					q.opts.Fault = o.Fault
					if _, err := q.Capture(t.Context(), r); err == nil {
						t.Fatal("accepted replacement source")
					}
					if !swapped {
						t.Fatal("attack callback not reached")
					}
					restore()
					statuses, err := q.Status(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if len(statuses) != 1 {
						t.Fatal("unsafe observation accepted job")
					}
				})
			}
		}
	}
}

func TestDerivedSQLiteAuthorizedRetainsWALPolicyAndRestores(t *testing.T) {
	dir, o, r := setup(t)
	r.Path = filepath.Join(filepath.Dir(r.Path), "session.sqlite")
	r.Transform = "sqlite-backup"
	db := sqliteQueueDB(t, r.Path, true, "uncheckpointed")
	alias := filepath.Join(filepath.Dir(r.Path), "wal-alias")
	if err := os.Symlink(r.Path+"-wal", alias); err != nil {
		t.Fatal(err)
	}
	// Move the real WAL behind an alias without checkpointing it.
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(r.Path+"-wal", alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alias, r.Path+"-wal"); err != nil {
		t.Fatal(err)
	}
	deny := false
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if deny && req.Path == alias {
			return ErrPolicy
		}
		return nil
	}
	q := open(t, dir, o)
	capture(t, q, r)
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(es[0].Identities, alias) {
		t.Fatal("WAL alias evidence lost")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{r.Path, r.Path + "-wal", alias} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	deny = true
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); !errors.Is(err, ErrPolicy) {
		t.Fatal("missing WAL origin bypassed revocation", err)
	}
	healthy := r
	healthy.Transform = ""
	healthy.Identity.SessionID = "healthy"
	healthy.Path = filepath.Join(filepath.Dir(r.Path), "healthy.jsonl")
	write(t, healthy.Path, "healthy")
	capture(t, q, healthy)
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, healthy.Identity); err != nil {
		t.Fatal(err)
	}
	deny = false
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	// Delete accepted spool bodies too: the reader must restore remote ownership.
	entries, err := os.ReadDir(filepath.Join(dir, "payloads"))
	if err == nil {
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(dir, "payloads", e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	dest := filepath.Join(t.TempDir(), "export")
	exported, err := publication.ExportSession(t.Context(), backend, r.Identity, dest)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", filepath.Join(dest, exported.Bodies[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	var count int
	if err := restored.QueryRowContext(t.Context(), "SELECT count(*) FROM evidence WHERE body='uncheckpointed'").Scan(&count); err != nil || count != 3 {
		t.Fatal("WAL rows lost", count, err)
	}
}

func TestDerivedSQLiteAuthorizedExactLeafScope(t *testing.T) {
	for _, mode := range []string{"absent", "undeclared", "declared"} {
		t.Run(mode, func(t *testing.T) {
			dir, o, r := setup(t)
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "logical")
			r.Path = filepath.Join(filepath.Dir(r.Path), "session.sqlite")
			r.Transform = "sqlite-backup"
			physical, err := filepath.EvalSymlinks(filepath.Dir(r.Path))
			if err != nil {
				t.Fatal(err)
			}
			r.Path = filepath.Join(physical, "session.sqlite")
			sqliteQueueDB(t, r.Path, mode != "absent", "safe")
			logical := filepath.Join(root, "session.sqlite")
			leaves := []capturecoordinates.LeafAlias{{Agent: "pi", LogicalPath: logical, Path: r.Path}}
			if mode == "declared" {
				leaves = append(leaves, capturecoordinates.LeafAlias{Agent: "pi", LogicalPath: logical + "-wal", Path: r.Path + "-wal"})
			}
			inv, err := capturecoordinates.Compile([]capturecoordinates.Root{{Agent: "pi", Path: root}}, nil, leaves)
			if err != nil {
				t.Fatal(err)
			}
			o.Coordinates = inv
			o.CheckPolicy = func(_ context.Context, req Request) error {
				_, err := inv.Coordinate(req.Identity.Agent, req.Path)
				return err
			}
			r.LogicalPath = logical
			q := open(t, dir, o)
			_, err = q.Capture(t.Context(), r)
			if mode == "undeclared" {
				if !errors.Is(err, ErrPolicy) {
					t.Fatal("sibling scope granted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := q.Deliver(t.Context(), newBackend(t), r.Identity); err != nil {
				t.Fatal("exact leaf delivery blocked", err)
			}
		})
	}
}

func TestDerivedSQLiteAuthorizedUnchangedRetainsNewWALAlias(t *testing.T) {
	dir, o, r := setup(t)
	r.Path = filepath.Join(filepath.Dir(r.Path), "session.sqlite")
	r.Transform = "sqlite-backup"
	sqliteQueueDB(t, r.Path, true, "safe")
	q := open(t, dir, o)
	first := capture(t, q, r)
	alias := filepath.Join(filepath.Dir(r.Path), "new-wal-origin")
	if e := os.Rename(r.Path+"-wal", alias); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(alias, r.Path+"-wal"); e != nil {
		t.Fatal(e)
	}
	next, e := q.CaptureIfChanged(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	if next.PublicationID == first.PublicationID || next.Sequence != first.Sequence+1 {
		t.Fatal("new WAL provenance discarded as no-op")
	}
	if len(next.Sources) != 1 || next.Sources[0].EpochID != first.Sources[0].EpochID || next.Sources[0].Revision != first.Sources[0].Revision+1 || next.Sources[0].Object != first.Sources[0].Object {
		t.Fatal("provenance acceptance changed bytes or source epoch")
	}
	again, e := q.CaptureIfChanged(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	if again.PublicationID != next.PublicationID {
		t.Fatal("retained WAL provenance prevents genuine no-op")
	}

	_, es, e := q.latest(t.Context(), r.Identity)
	if e != nil {
		t.Fatal(e)
	}
	if !slices.Contains(es[0].Identities, alias) {
		t.Fatalf("new present WAL origin lost on unchanged capture: %q", es[0].Identities)
	}
}

func TestDerivedSQLiteAuthorizedUnchangedMissingOriginRevocation(t *testing.T) {
	dir, o, r := setup(t)
	r.Path = filepath.Join(filepath.Dir(r.Path), "session.sqlite")
	r.Transform = "sqlite-backup"
	db := sqliteQueueDB(t, r.Path, true, "safe")
	alias := filepath.Join(filepath.Dir(r.Path), "new-wal-origin")
	deny := false
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if deny && req.Path == alias {
			return ErrPolicy
		}
		return nil
	}
	q, e := Open(t.Context(), dir, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if q != nil {
			if e := q.Close(); e != nil {
				t.Error(e)
			}
		}
	})
	capture(t, q, r)
	if e := os.Rename(r.Path+"-wal", alias); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(alias, r.Path+"-wal"); e != nil {
		t.Fatal(e)
	}
	if _, e := q.CaptureIfChanged(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{r.Path, r.Path + "-wal", alias} {
		if e := os.Remove(p); e != nil && !errors.Is(e, os.ErrNotExist) {
			t.Fatal(e)
		}
	}
	if e := q.Close(); e != nil {
		t.Fatal(e)
	}
	q = nil
	q, e = Open(t.Context(), dir, o)
	if e != nil {
		t.Fatal(e)
	}
	deny = true
	if e := q.Deliver(t.Context(), newBackend(t), r.Identity); !errors.Is(e, ErrPolicy) {
		t.Fatalf("missing newly observed WAL origin bypassed revocation: %v", e)
	}
	healthy := r
	healthy.Transform = ""
	healthy.Identity.SessionID = "healthy-after-reopen"
	healthy.Path = filepath.Join(filepath.Dir(r.Path), "healthy.jsonl")
	write(t, healthy.Path, "healthy")
	capture(t, q, healthy)
	if e := q.Deliver(t.Context(), newBackend(t), healthy.Identity); e != nil {
		t.Fatal("revoked session blocked healthy publication", e)
	}
}
