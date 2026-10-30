package state

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestArchiveSnapshotValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sequences []uint64
		parents   []string
		want      string
	}{
		{"empty", nil, nil, ""},
		{"valid", []uint64{1, 2}, []string{"", "id-0"}, ""},
		{"gap", []uint64{1, 3}, []string{"", "id-0"}, "divergent or incomplete"},
		{"branch", []uint64{1, 2, 2}, []string{"", "id-0", "id-0"}, "divergent or incomplete"},
		{"missing", []uint64{2}, []string{"id-0"}, "divergent or incomplete"},
		{"rootParent", []uint64{1}, []string{"other"}, "missing or conflicting parent"},
		{"parent", []uint64{1, 2}, []string{"", "other"}, "missing or conflicting parent"},
		// Count=max alone does not establish continuity.
		{"balancedBranchGap", []uint64{1, 1, 3}, []string{"", "", "id-0"}, "divergent or incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := open(t)
			check(t, db.Atomic(func() error {
				for i, seq := range tc.sequences {
					if err := db.RecordArchive(Record{Key: strconv.Itoa(i), Kind: "file", Stream: "s", ID: fmt.Sprintf("id-%d", i), Sequence: seq, Parent: tc.parents[i], Committed: true, Body: []byte("body")}); err != nil {
						return err
					}
				}
				// A disconnected plan must not affect the committed selection.
				return db.RecordArchive(Record{Key: "plan", Kind: "file", Stream: "s", ID: "plan", Sequence: 100, Body: []byte("plan")})
			}))
			got, err := db.CurrentArchive("file", "s")
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("CurrentArchive = %+v, %v", got, err)
				}
				return
			}
			check(t, err)
			if len(tc.sequences) == 0 {
				if got != nil {
					t.Fatal("empty chain selected a plan")
				}
				return
			}
			if got == nil || got.Sequence != 2 || got.ID != "id-1" {
				t.Fatalf("tip = %+v", got)
			}
		})
	}
}

func TestArchiveMetadataAndImmutableRollback(t *testing.T) {
	db := open(t)
	r := Record{Key: "record", Kind: "file", Stream: "s", ID: "a", Sequence: 1, Committed: true, Body: []byte("original")}
	check(t, db.ImportPlan(r))
	size, found, err := db.ArchiveRecordSize(r.Key)
	check(t, err)
	if !found || size != 8 {
		t.Fatalf("size = %d, %v", size, found)
	}
	_, found, err = db.ArchiveRecordSize("missing")
	check(t, err)
	if found {
		t.Fatal("missing record exists")
	}
	check(t, db.ImportPlan(Record{Key: "empty", Body: []byte{}}))
	size, found, err = db.ArchiveRecordSize("empty")
	check(t, err)
	if !found || size != 0 {
		t.Fatal("empty body treated as missing")
	}
	err = db.Atomic(func() error {
		if err := db.MarkArchiveApplied(r.Key); err != nil {
			return err
		}
		r.Body = []byte("corrupt")
		return db.RecordArchive(r)
	})
	if err == nil {
		t.Fatal("immutable corruption accepted")
	}
	applied, err := db.ArchiveApplied(r.Key)
	check(t, err)
	if applied {
		t.Fatal("failed operation leaked marker")
	}
	tip, err := db.CurrentArchive("file", "s")
	check(t, err)
	if string(tip.Body) != "original" {
		t.Fatal("original body changed")
	}
}

func TestArchiveSeenBatchBoundAndRollback(t *testing.T) {
	db := open(t)
	keys := make([]string, ArchiveSeenBatchSize)
	check(t, db.Atomic(func() error {
		for i := range keys {
			keys[i] = fmt.Sprintf("prefix/%04d", i)
			if err := db.RecordArchive(Record{Key: keys[i], Body: []byte{}}); err != nil {
				return err
			}
		}
		return nil
	}))
	check(t, db.MarkArchiveSeenBatch(keys, "old"))
	missing, err := db.MissingArchive("prefix/", "old")
	check(t, err)
	if missing != "" {
		t.Fatalf("missing %s", missing)
	}
	if err := db.MarkArchiveSeenBatch(append(keys, "extra"), "new"); err == nil {
		t.Fatal("oversized batch accepted")
	}
	// A SQLite failure halfway through must roll back earlier marker replacements.
	check(t, db.exec("CREATE TRIGGER fail_scan BEFORE INSERT ON archive_scan WHEN NEW.key='prefix/0500' BEGIN SELECT RAISE(ABORT,'injected'); END"))
	check(t, db.Flush())
	if err := db.MarkArchiveSeenBatch(keys, "new"); err == nil {
		t.Fatal("injected marker failure accepted")
	}
	missing, err = db.MissingArchive("prefix/", "old")
	check(t, err)
	if missing != "" {
		t.Fatalf("batch partially committed: %s", missing)
	}
	check(t, db.MarkArchiveSeenBatch(nil, "unused"))
}

func TestWalkArchiveAppliedWithCallbackWrites(t *testing.T) {
	db := open(t)
	check(t, db.Atomic(func() error {
		for i := range 4 {
			if err := db.RecordArchive(Record{Key: strconv.Itoa(i), Kind: "file", Stream: "s", ID: strconv.Itoa(i), Sequence: uint64(i + 1), Committed: true, Body: []byte("body")}); err != nil {
				return err
			}
		}
		return db.MarkArchiveApplied("1")
	}))
	count := 0
	check(t, db.WalkArchive("file", "s", func(r Record) error {
		if r.Applied != (r.Key == "1") {
			return fmt.Errorf("wrong applied flag: %+v", r)
		}
		count++
		return db.Atomic(func() error { return db.MarkArchiveApplied(r.Key) })
	}))
	if count != 4 {
		t.Fatalf("visited %d", count)
	}
	check(t, db.WalkArchive("file", "s", func(r Record) error {
		if !r.Applied {
			return errors.New("callback marker lost")
		}
		return nil
	}))
	sentinel := errors.New("stop")
	if err := db.WalkArchive("file", "s", func(Record) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error = %v", err)
	}
}

func TestArchiveRecordSizesBoundedSnapshot(t *testing.T) {
	db := open(t)
	keys := make([]string, ArchiveSeenBatchSize)
	check(t, db.Atomic(func() error {
		for i := range keys {
			keys[i] = fmt.Sprintf("sizes/%04d", i)
			if err := db.RecordArchive(Record{Key: keys[i], Body: []byte(strings.Repeat("x", i))}); err != nil {
				return err
			}
		}
		return nil
	}))
	prepared := len(db.stmts)
	sizes, err := db.ArchiveRecordSizes(keys)
	check(t, err)
	if len(sizes) != len(keys) {
		t.Fatalf("got %d sizes, want %d", len(sizes), len(keys))
	}
	for i, key := range keys {
		if size, found := sizes[key]; !found || size != int64(i) {
			t.Fatalf("size[%s] = %d, %v", key, size, found)
		}
	}
	sizes, err = db.ArchiveRecordSizes([]string{keys[0], keys[0], "missing", keys[999]})
	check(t, err)
	if len(sizes) != 2 || sizes[keys[0]] != 0 || sizes[keys[999]] != 999 {
		t.Fatalf("duplicate/missing/zero sizes = %v", sizes)
	}
	if _, found := sizes["missing"]; found {
		t.Fatal("missing key returned as zero")
	}
	if len(db.stmts) != prepared {
		t.Fatal("variable SQL retained in statement cache")
	}
	if _, err := db.ArchiveRecordSizes(append(keys, "overflow")); err == nil {
		t.Fatal("oversized size batch accepted")
	}
	sizes, err = db.ArchiveRecordSizes(nil)
	check(t, err)
	if len(sizes) != 0 {
		t.Fatal("empty request returned records")
	}
	// No cursor/transaction may survive the lookup into the next domain operation.
	check(t, db.SetCheckpoint("after-read", false))
	check(t, db.Close())
	if _, err := db.ArchiveRecordSizes(keys[:1]); err == nil {
		t.Fatal("closed database read succeeded")
	}
}
