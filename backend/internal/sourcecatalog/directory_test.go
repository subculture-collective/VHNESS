package sourcecatalog

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCompleteDirectoryBeyondFacets(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i := 0; i < 211; i++ {
		id := fmt.Sprintf("dir%03d", i)
		raw := strings.ReplaceAll(string(fixture(id, fmt.Sprintf("Room %03d", i))), "Ween", fmt.Sprintf("Artist %03d", i))
		if err := s.Import(ctx, id, []byte(raw), 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	listed := false
	if err := s.SetListing(ctx, ListingDecision{Identifiers: []string{"dir210"}, Listed: &listed}, "Exclude directory fixture"); err != nil {
		t.Fatal(err)
	}
	for _, dimension := range []string{"artists", "venues"} {
		for _, order := range []string{"name_asc", "name_desc", "count_desc"} {
			t.Run(dimension+order, func(t *testing.T) {
				q := DirectoryQuery{Dimension: dimension, Sort: order, Limit: 37}
				seen := map[string]bool{}
				for n := 0; ; n++ {
					if n > 6 {
						t.Fatal("cursor did not terminate")
					}
					page, err := s.Directory(ctx, q)
					if err != nil || page.Total != 210 || len(page.Items) > 37 {
						t.Fatal(page, err)
					}
					for _, f := range page.Items {
						if seen[f.Value] || f.Count != 1 {
							t.Fatal("duplicate or wrong count", f)
						}
						seen[f.Value] = true
					}
					if page.Cursor == "" {
						break
					}
					changed := q
					changed.Search, changed.Cursor = "changed", page.Cursor
					if _, err := s.Directory(ctx, changed); err == nil {
						t.Fatal("changed query accepted")
					}
					q.Cursor = page.Cursor
				}
				if len(seen) != 210 {
					t.Fatal("directory incomplete", len(seen))
				}
			})
		}
	}
	page, err := s.Directory(ctx, DirectoryQuery{Dimension: "artists", Search: "ARTIST 209"})
	if err != nil || page.Total != 1 || page.Items[0].Value != "Artist 209" {
		t.Fatal("entry beyond top 200 not discoverable", page, err)
	}
	years, err := s.Directory(ctx, DirectoryQuery{Dimension: "years"})
	if err != nil || years.Total != 1 || years.Items[0].Value != "2003" || years.Items[0].Count != 210 {
		t.Fatal(years, err)
	}
	other := &Store{Core: s.Core, Collection: "absent"}
	page, err = other.Directory(ctx, DirectoryQuery{Dimension: "artists"})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("collection leaked", page, err)
	}
}

func TestDirectoryBoundsAndCursorValidation(t *testing.T) {
	for _, q := range []DirectoryQuery{{}, {Dimension: "invalid"}, {Dimension: "artists", Sort: "invalid"}, {Dimension: "artists", Limit: 101}, {Dimension: "artists", Limit: -1}, {Dimension: "venues", Search: strings.Repeat("x", 301)}, {Dimension: "years", Cursor: "invalid"}} {
		if q.Validate() == nil {
			t.Fatal("invalid directory query accepted", q)
		}
	}
}

func TestDirectoryNamesCountsAndCursorBinding(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	// Exact public labels are retained, including punctuation and accents.
	for i, artist := range []string{"Björk", "Björk", "Björk", "R.E.M.", "R.E.M.", "Alpha", "Alpha", "Café", "Excluded"} {
		id := fmt.Sprintf("names%02d", i)
		raw := strings.ReplaceAll(string(fixture(id, artist+" Hall")), "Ween", artist)
		if err := s.Import(ctx, id, []byte(raw), 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	listed := false
	if err := s.SetListing(ctx, ListingDecision{Identifiers: []string{"names08"}, Listed: &listed}, "Excluded-only directory entry"); err != nil {
		t.Fatal(err)
	}
	q := DirectoryQuery{Dimension: "artists", Sort: "count_desc", Limit: 2}
	var got []Facet
	for {
		page, err := s.Directory(ctx, q)
		if err != nil || page.Total != 4 {
			t.Fatal(page, err)
		}
		got = append(got, page.Items...)
		if page.Cursor == "" {
			break
		}
		for _, changed := range []DirectoryQuery{
			{Dimension: "venues", Sort: q.Sort, Limit: q.Limit, Cursor: page.Cursor},
			{Dimension: q.Dimension, Sort: "name_asc", Limit: q.Limit, Cursor: page.Cursor},
			{Dimension: q.Dimension, Sort: q.Sort, Limit: 3, Cursor: page.Cursor},
		} {
			if _, err := s.Directory(ctx, changed); err == nil {
				t.Fatal("changed directory cursor accepted", changed)
			}
		}
		q.Cursor = page.Cursor
	}
	want := []Facet{{Value: "Björk", Count: 3}, {Value: "Alpha", Count: 2}, {Value: "R.E.M.", Count: 2}, {Value: "Café", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("count/tie order", got, want)
	}
	for _, tc := range []struct {
		query string
		want  []Facet
	}{
		{"JÖ", []Facet{{Value: "Björk", Count: 3}}},
		{"jör", []Facet{{Value: "Björk", Count: 3}}},
		{"bjork", []Facet{}},
		{"R.E.", []Facet{{Value: "R.E.M.", Count: 2}}},
		{"REM", []Facet{}},
		{"%", []Facet{}},
		{"_", []Facet{}},
		{"Excluded", []Facet{}},
	} {
		page, err := s.Directory(ctx, DirectoryQuery{Dimension: "artists", Search: tc.query})
		if err != nil || !reflect.DeepEqual(page.Items, tc.want) {
			t.Fatal(tc.query, page, err)
		}
	}
	for _, order := range []string{"name_asc", "name_desc"} {
		var values []string
		q := DirectoryQuery{Dimension: "artists", Sort: order, Limit: 2}
		for {
			page, err := s.Directory(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range page.Items {
				values = append(values, f.Value)
			}
			if page.Cursor == "" {
				break
			}
			q.Cursor = page.Cursor
		}
		expected := []string{"Alpha", "Björk", "Café", "R.E.M."}
		if order == "name_desc" {
			expected = []string{"R.E.M.", "Café", "Björk", "Alpha"}
		}
		if !reflect.DeepEqual(values, expected) {
			t.Fatal(order, values)
		}
	}
}
