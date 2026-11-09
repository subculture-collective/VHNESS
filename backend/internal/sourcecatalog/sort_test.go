package sourcecatalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestRecordingOrdersAcrossPages(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	type record struct{ id, artist, date string }
	var records []record
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("sort%03d", i)
		artist := []string{"Zed", "alpha", "Alpha", ""}[i%4]
		date := fmt.Sprintf("200%d-09-23", i%3)
		if i == 204 {
			date = "unknown"
		}
		raw := strings.ReplaceAll(string(fixture(id, "Room")), "Ween", artist)
		raw = strings.ReplaceAll(raw, "2003-09-23", date)
		if err := s.Import(ctx, id, []byte(raw), 200, "", ""); err != nil {
			t.Fatal(err)
		}
		if i != 5 {
			records = append(records, record{id, artist, date})
		}
	}
	listed := false
	if err := s.SetListing(ctx, ListingDecision{Identifiers: []string{"sort005"}, Listed: &listed}, "Excluded sort fixture"); err != nil {
		t.Fatal(err)
	}
	for _, order := range []string{"", "date_desc", "date_asc", "artist_asc"} {
		t.Run(order, func(t *testing.T) {
			var want []record
			for _, r := range records {
				if (order == "date_desc" || order == "date_asc") && !validCompleteDate(r.date) {
					continue
				}
				want = append(want, r)
			}
			sort.Slice(want, func(i, j int) bool {
				a, b := want[i], want[j]
				if order == "date_desc" && a.date != b.date {
					return a.date > b.date
				}
				if order == "date_asc" && a.date != b.date {
					return a.date < b.date
				}
				if order == "artist_asc" && strings.ToLower(a.artist) != strings.ToLower(b.artist) {
					return strings.ToLower(a.artist) < strings.ToLower(b.artist)
				}
				return a.id < b.id
			})
			q := Query{Sort: order}
			var got []string
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > 3 {
					t.Fatal("cursor failed to terminate")
				}
				page, err := s.Search(ctx, q)
				if err != nil || page.Total != int64(len(want)) {
					t.Fatalf("page: %v, total %d, want %d", err, page.Total, len(want))
				}
				for _, item := range page.Items {
					if _, ok := item["_cursor_artist"]; ok {
						t.Fatal("internal sort key leaked")
					}
					got = append(got, item["identifier"].(string))
				}
				if page.Cursor == "" {
					break
				}
				if _, err := s.Search(ctx, Query{Sort: order, Venue: "changed", Cursor: page.Cursor}); err == nil {
					t.Fatal("accepted changed filters")
				}
				otherOrder := "artist_asc"
				if otherOrder == order {
					otherOrder = "date_asc"
				}
				if _, err := s.Search(ctx, Query{Sort: otherOrder, Cursor: page.Cursor}); err == nil {
					t.Fatal("accepted changed order")
				}
				q.Cursor = page.Cursor
			}
			if len(got) != len(want) {
				t.Fatalf("got %d rows, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i].id {
					t.Fatalf("row %d: got %s, want %s", i, got[i], want[i].id)
				}
			}
		})
	}
	for _, order := range []string{"date_desc", "date_asc", "artist_asc"} {
		page, err := s.Search(ctx, Query{Sort: order, Artist: "ALPHA", Venue: "roo", YearFrom: "2001", YearTo: "2002", MonthDay: "09-23"})
		var expected int64
		for _, r := range records {
			if strings.EqualFold(r.artist, "alpha") && r.date >= "2001-01-01" && r.date <= "2002-12-31" {
				expected++
			}
		}
		if err != nil || page.Total != expected || len(page.Items) != int(expected) {
			t.Fatal("mixed filters", order, page.Total, expected, err)
		}
	}

}

func TestRecordingOrderValidation(t *testing.T) {
	for _, order := range []string{"", "date_desc", "date_asc", "artist_asc"} {
		if err := (Query{Sort: order}).Validate(); err != nil {
			t.Fatal(order, err)
		}
	}
	for _, q := range []Query{{Sort: "invalid"}, {Sort: "date_asc", Cursor: "invalid"}, {Sort: "artist_asc", Cursor: "invalid"}} {
		if q.Validate() == nil && ValidateCursor(q) == nil {
			t.Fatal("invalid order/cursor accepted", q)
		}
	}
}

func TestRecordingSortPlans(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	if err := s.Import(ctx, "planbase", fixture("planbase", "Room"), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	// Synthetic projections, independent of the production catalog.
	if _, err := s.Core.Pool.Exec(ctx, `INSERT INTO source_catalog_items(collection_id,identifier,source_hash,normalized,normalization,effective,search)
 SELECT collection_id,'plan'||lpad(i::text,5,'0'),source_hash,normalized,normalization,'{}',''
 FROM source_catalog_items CROSS JOIN generate_series(1,5000) i WHERE identifier='planbase'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Core.Pool.Exec(ctx, "ANALYZE source_catalog_items"); err != nil {
		t.Fatal(err)
	}
	for _, order := range []string{"date_desc", "date_asc", "artist_asc"} {
		rows, err := s.Core.Pool.Query(ctx, "EXPLAIN (ANALYZE,BUFFERS) "+recordingSearchSQL, "", "", "", "", "", "", "", "", s.Collection, "", "", "", order, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		t.Log(order + "\n" + strings.Join(lines, "\n"))
	}
}
