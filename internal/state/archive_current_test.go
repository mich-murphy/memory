package state

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCurrentArchivePageReferenceParity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seqs    []uint64
		parents []string
	}{
		{"plansOnly", nil, nil},
		{"valid", []uint64{1, 2}, []string{"", "id-0"}},
		{"zero", []uint64{0}, []string{""}},
		{"gap", []uint64{1, 3}, []string{"", "id-0"}},
		{"branch", []uint64{1, 2, 2}, []string{"", "id-0", "id-0"}},
		{"balancedBranchGap", []uint64{1, 1, 3}, []string{"", "", "id-0"}},
		{"rootParent", []uint64{1}, []string{"wrong"}},
		{"wrongParent", []uint64{1, 2}, []string{"", "wrong"}},
	} {
		for _, stream := range []string{"", "s"} {
			t.Run(tc.name+"/"+stream, func(t *testing.T) {
				db := open(t)
				check(t, db.Atomic(func() error {
					for i, seq := range tc.seqs {
						r := Record{Key: strconv.Itoa(i), Kind: "file", Stream: stream, ID: fmt.Sprintf("id-%d", i), Parent: tc.parents[i], Sequence: seq, Committed: true, Body: []byte("synthetic")}
						if err := db.RecordArchive(r); err != nil {
							return err
						}
					}
					return db.RecordArchive(Record{Key: "plan", Kind: "file", Stream: stream, Sequence: 100, Body: []byte("ignored")})
				}))
				want, wantErr := db.CurrentArchive("file", stream)
				got, err := db.CurrentArchivePage("file", nil)
				if fmt.Sprint(err) != fmt.Sprint(wantErr) {
					t.Fatalf("errors: %v != %v", err, wantErr)
				}
				if err != nil {
					return
				}
				if want == nil {
					if len(got) != 0 {
						t.Fatal("selected plan")
					}
				} else if len(got) != 1 || !reflect.DeepEqual(got[0], *want) {
					t.Fatalf("page differs from reference: %v", got)
				}
			})
		}
	}
}

func TestCurrentArchivePageBoundariesAndFreshness(t *testing.T) {
	db := open(t)
	check(t, db.Atomic(func() error {
		for i := range 130 {
			stream := ""
			if i > 0 {
				stream = fmt.Sprintf("s-%03d", i)
			}
			if err := db.RecordArchive(Record{Key: "key-" + stream, Kind: "file", Stream: stream, ID: "first", Sequence: 1, Committed: true, Body: []byte("tip")}); err != nil {
				return err
			}
		}
		return nil
	}))
	var after *string
	var all []Record
	for _, size := range []int{64, 64, 2, 0} {
		page, err := db.CurrentArchivePage("file", after)
		check(t, err)
		if len(page) != size {
			t.Fatalf("page size = %d, want %d", len(page), size)
		}
		all = append(all, page...)
		if len(page) > 0 {
			last := page[len(page)-1].Stream
			after = &last
		}
		// A write on this same single SQL connection would hang with an open cursor.
		check(t, db.SetCheckpoint("cursor-probe", false))
	}
	if all[0].Stream != "" {
		t.Fatal("nil boundary skipped empty stream")
	}
	empty := ""
	page, err := db.CurrentArchivePage("file", &empty)
	check(t, err)
	if page[0].Stream != "s-001" {
		t.Fatal("empty boundary not exclusive")
	}
	check(t, db.Atomic(func() error {
		return db.RecordArchive(Record{Key: "new", Kind: "file", Stream: "", ID: "second", Parent: "first", Sequence: 2, Committed: true, Body: []byte("fresh")})
	}))
	page, err = db.CurrentArchivePage("file", nil)
	check(t, err)
	if string(page[0].Body) != "fresh" {
		t.Fatal("stale cached tip")
	}
}

func TestCurrentArchivePageBodyBudget(t *testing.T) {
	db := open(t)
	body := []byte(strings.Repeat("x", 1<<20))
	check(t, db.Atomic(func() error {
		for i := range 4 {
			if err := db.RecordArchive(Record{Key: strconv.Itoa(i), Kind: "file", Stream: strconv.Itoa(i), ID: "id", Sequence: 1, Committed: true, Body: body}); err != nil {
				return err
			}
		}
		return nil
	}))
	for _, budget := range []int{1, 2 << 20} {
		// Check the SQL result itself, not only the Go page accumulator: an
		// outer sorter must not first materialize every candidate tip body.
		selected := func() int {
			rows, err := db.conn.QueryContext(db.ctx, currentArchivePageQuery, "file", nil, nil, nil, "file", "file", budget)
			check(t, err)
			defer func() { check(t, rows.Close()) }()
			n := 0
			for rows.Next() {
				n++
			}
			check(t, rows.Err())
			return n
		}()
		wantSelected := 1
		if budget == 2<<20 {
			wantSelected = 2
		}
		if selected != wantSelected {
			t.Fatalf("SQL fetched %d bodies before applying byte budget %d", selected, budget)
		}
		var after *string
		n := 0
		for {
			page, err := db.currentArchivePage("file", after, budget)
			check(t, err)
			if len(page) == 0 {
				break
			}
			wantSize := 1
			if budget == 2<<20 {
				wantSize = 2
			}
			if len(page) != wantSize {
				t.Fatalf("budget %d: size %d", budget, len(page))
			}
			n += len(page)
			last := page[len(page)-1].Stream
			after = &last
			check(t, db.SetCheckpoint("overflow-cursor-probe", false))
		}
		if n != 4 {
			t.Fatalf("overflow skipped a row: %d", n)
		}
	}
}

func TestCurrentArchivePageCoveringPlan(t *testing.T) {
	db := open(t)
	rows, err := db.conn.QueryContext(db.ctx, "EXPLAIN QUERY PLAN "+currentArchivePageQuery, "file", nil, nil, nil, "file", "file", archiveCurrentPageBytes)
	check(t, err)
	defer func() { _ = rows.Close() }()
	covering := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		check(t, rows.Scan(&id, &parent, &unused, &detail))
		if strings.Contains(detail, "COVERING INDEX archive_current") {
			covering++
		}
	}
	check(t, rows.Err())
	if covering < 3 {
		t.Fatalf("chain and parent validation not covered: %d", covering)
	}
}
