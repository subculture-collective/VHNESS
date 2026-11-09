package sourcecatalog

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSeededSampleVisibilityStabilityAndBounds(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i := 0; i < 220; i++ {
		id := fmt.Sprintf("sample%03d", i)
		if err := s.Import(ctx, id, fixture(id, "Room"), 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	q := SampleQuery{Seed: "daily-example", Limit: 20}
	first, err := s.Sample(ctx, q)
	if err != nil || len(first.Items) != 20 {
		t.Fatal(first, err)
	}
	again, err := s.Sample(ctx, q)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("same seed changed", err)
	}
	changed, err := s.Sample(ctx, SampleQuery{Seed: "explicit-new-seed", Limit: 20})
	if err != nil || reflect.DeepEqual(first.Items, changed.Items) {
		t.Fatal("new seed did not change this sufficiently large sample", err)
	}
	seen, beyondFirstPage := map[string]bool{}, false
	for _, item := range first.Items {
		id := item["identifier"].(string)
		if seen[id] {
			t.Fatal("duplicate sample", id)
		}
		seen[id] = true
		if id >= "sample100" {
			beyondFirstPage = true
		}
		if item["source"] == nil || item["lineage"] == nil || item["creator"] == nil {
			t.Fatal("card source/artist fields missing")
		}
	}
	if !beyondFirstPage {
		t.Fatal("sample only used first identifier page")
	}
	listed := false
	excluded := first.Items[0]["identifier"].(string)
	if err := s.SetListing(ctx, ListingDecision{Identifiers: []string{excluded}, Listed: &listed}, "Exclude sampled fixture"); err != nil {
		t.Fatal(err)
	}
	page, err := s.Sample(ctx, q)
	if err != nil || len(page.Items) != 20 {
		t.Fatal(page, err)
	}
	for _, item := range page.Items {
		if item["identifier"] == excluded {
			t.Fatal("excluded item returned from stable seed")
		}
	}
	other := &Store{Core: s.Core, Collection: "other"}
	page, err = other.Sample(ctx, q)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("collection leaked", page, err)
	}
	// A 50-result request on a one-record collection wraps once without repeats.
	small := testDB(t)
	if err := small.Import(ctx, "only", fixture("only", "Room"), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	page, err = small.Sample(ctx, SampleQuery{Seed: "one", Limit: 50})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
}

func TestSampleValidation(t *testing.T) {
	for _, q := range []SampleQuery{{}, {Seed: "bad seed"}, {Seed: "ok", Limit: 51}, {Seed: "ok", Limit: -1}} {
		if q.Validate() == nil {
			t.Fatal("invalid sample accepted", q)
		}
	}
}

func TestSamplePlanUsesBoundedIndexRanges(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	if err := s.Import(ctx, "planbase", fixture("planbase", "Room"), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	// Synthetic projection rows reuse one retained fixture, never a production snapshot.
	if _, err := s.Core.Pool.Exec(ctx, `INSERT INTO source_catalog_items(collection_id,identifier,source_hash,normalized,normalization,effective,search)
 SELECT collection_id,'plan'||lpad(i::text,5,'0'),source_hash,normalized,normalization,'{}',''
 FROM source_catalog_items CROSS JOIN generate_series(1,10000) i WHERE identifier='planbase'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Core.Pool.Exec(ctx, "ANALYZE source_catalog_items"); err != nil {
		t.Fatal(err)
	}
	for _, anchor := range []string{"00000000000000000000000000000000", "ffffffffffffffffffffffffffffffff"} {
		rows, err := s.Core.Pool.Query(ctx, "EXPLAIN (ANALYZE,BUFFERS) "+sampleSQL, s.Collection, anchor, 20)
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
		plan := strings.Join(lines, "\n")
		t.Log(plan)
		if !strings.Contains(plan, "source_catalog_visible_sample_idx") || strings.Contains(plan, "Seq Scan on source_catalog_items") {
			t.Fatal("sample lost bounded index ranges", plan)
		}
		page, err := s.Sample(ctx, SampleQuery{Seed: "large-plan", Limit: 20})
		if err != nil || len(page.Items) != 20 {
			t.Fatal(page, err)
		}
	}
}
