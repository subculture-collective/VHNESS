package sourcecatalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func popularityBatch(at time.Time, response string, ids ...string) PopularityBatch {
	return PopularityBatch{Provider: "archive_views", ObservedAt: at, Requested: ids, Response: []byte(response)}
}

func importAll(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := s.Import(context.Background(), id, fixture(id, "Room"), 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func popularityOf(t *testing.T, s *Store, id string) (Popularity, bool) {
	t.Helper()
	found, err := s.Popularity(context.Background(), "archive_views", []string{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		return Popularity{}, false
	}
	return found[0], true
}

func TestPopularityDistinguishesZeroMissingAndAbsent(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	importAll(t, s, "zero", "missing", "absent")
	now := time.Now().UTC().Truncate(time.Second)
	response := `{"zero":{"have_data":true,"all_time":42,"last_30day":0,"last_7day":0},"missing":{"have_data":false,"all_time":0,"last_30day":0,"last_7day":0}}`
	report, err := s.ImportPopularity(ctx, popularityBatch(now, response, "zero", "missing", "absent", "not-in-catalog"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Usable != 1 || report.NoData != 1 || report.Omitted != 1 || report.UnknownItems != 1 || !report.ResponseValid || report.Requested != 4 {
		t.Fatalf("%+v", report)
	}
	zero, ok := popularityOf(t, s, "zero")
	if !ok || !zero.HaveData || *zero.AllTime != 42 || *zero.Last30Day != 0 || *zero.Last7Day != 0 || !zero.Fresh {
		t.Fatalf("observed zero lost: %+v", zero)
	}
	missing, ok := popularityOf(t, s, "missing")
	if !ok || missing.HaveData || missing.AllTime != nil || missing.Fresh || missing.LastOutcome != "no_data" {
		t.Fatalf("missing data became a count: %+v", missing)
	}
	if _, ok := popularityOf(t, s, "absent"); ok {
		t.Fatal("an omitted identifier must not gain a snapshot")
	}
}

func TestPopularityRejectsMalformedCounters(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	importAll(t, s, "show")
	now := time.Now().UTC()
	good := `{"show":{"have_data":true,"all_time":8,"last_30day":3,"last_7day":1}}`
	if _, err := s.ImportPopularity(ctx, popularityBatch(now.Add(-time.Hour), good, "show")); err != nil {
		t.Fatal(err)
	}
	for i, bad := range []string{
		`{"have_data":true,"all_time":-1,"last_30day":0,"last_7day":0}`,
		`{"have_data":true,"all_time":0.5,"last_30day":0,"last_7day":0}`,
		`{"have_data":true,"all_time":"42","last_30day":0,"last_7day":0}`,
		`{"have_data":true,"all_time":9223372036854775808,"last_30day":0,"last_7day":0}`,
		`{"have_data":true,"all_time":1e3,"last_30day":0,"last_7day":0}`,
		`{"have_data":true,"all_time":42,"last_30day":0}`,
		`{"have_data":"true","all_time":1,"last_30day":0,"last_7day":0}`,
		`[]`,
	} {
		response := fmt.Sprintf(`{"show":%s}`, bad)
		report, err := s.ImportPopularity(ctx, popularityBatch(now.Add(time.Duration(i-30)*time.Minute), response, "show"))
		if err != nil || report.Malformed != 1 || report.Usable != 0 {
			t.Fatalf("%s: %+v %v", bad, report, err)
		}
		kept, _ := popularityOf(t, s, "show")
		if *kept.AllTime != 8 || kept.LastOutcome != "malformed" {
			t.Fatalf("%s replaced usable counts: %+v", bad, kept)
		}
	}
}

func TestPopularityPartialFailureKeepsUsableAndRetainsEvidence(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	importAll(t, s, "one", "two")
	now := time.Now().UTC().Truncate(time.Second)
	first := `{"one":{"have_data":true,"all_time":100,"last_30day":5,"last_7day":2},"two":{"have_data":true,"all_time":7,"last_30day":1,"last_7day":0}}`
	if _, err := s.ImportPopularity(ctx, popularityBatch(now.Add(-2*time.Hour), first, "one", "two")); err != nil {
		t.Fatal(err)
	}
	partial := `{"one":{"have_data":true,"all_time":101,"last_30day":6,"last_7day":3}}`
	report, err := s.ImportPopularity(ctx, popularityBatch(now.Add(-time.Hour), partial, "one", "two"))
	if err != nil || report.Usable != 1 || report.Omitted != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	two, _ := popularityOf(t, s, "two")
	if *two.AllTime != 7 || two.LastOutcome != "omitted" {
		t.Fatalf("omission changed counts: %+v", two)
	}
	broken := `{not-valid-json`
	report, err = s.ImportPopularity(ctx, popularityBatch(now.Add(-30*time.Minute), broken, "one"))
	if err != nil || report.ResponseValid || report.Malformed != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	one, _ := popularityOf(t, s, "one")
	if *one.AllTime != 101 || one.LastOutcome != "malformed" {
		t.Fatalf("unparseable response changed counts: %+v", one)
	}
	if report, err = s.ImportPopularity(ctx, popularityBatch(now.Add(-time.Minute), `{"one":{"have_data":false,"all_time":0,"last_30day":0,"last_7day":0}}`, "one")); err != nil || report.NoData != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	one, _ = popularityOf(t, s, "one")
	if !one.HaveData || *one.AllTime != 101 || one.LastOutcome != "no_data" {
		t.Fatalf("no-data response replaced usable counts: %+v", one)
	}
	var raw []byte
	var valid bool
	var kept int
	if err := s.Core.Pool.QueryRow(ctx, `SELECT raw,valid,(SELECT count(*) FROM source_popularity_responses) FROM source_popularity_responses WHERE sha256=$1`, report.ResponseSHA256).Scan(&raw, &valid, &kept); err != nil {
		t.Fatal(err)
	}
	if !valid || kept != 4 {
		t.Fatalf("every dated response must remain: valid=%v kept=%d", valid, kept)
	}
	var brokenRaw []byte
	if err := s.Core.Pool.QueryRow(ctx, `SELECT raw FROM source_popularity_responses WHERE NOT valid`).Scan(&brokenRaw); err != nil || string(brokenRaw) != broken {
		t.Fatalf("exact invalid response not retained: %q %v", brokenRaw, err)
	}
}

func TestPopularityStaleAndOutOfOrderBatches(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	importAll(t, s, "show")
	now := time.Now().UTC().Truncate(time.Second)
	counts := func(all int) string {
		return fmt.Sprintf(`{"show":{"have_data":true,"all_time":%d,"last_30day":0,"last_7day":0}}`, all)
	}
	if _, err := s.ImportPopularity(ctx, popularityBatch(now.Add(-48*time.Hour), counts(10), "show")); err != nil {
		t.Fatal(err)
	}
	stale, _ := popularityOf(t, s, "show")
	if stale.Fresh || !stale.HaveData || *stale.AllTime != 10 {
		t.Fatalf("stale counts should remain without a freshness claim: %+v", stale)
	}
	if _, err := s.ImportPopularity(ctx, popularityBatch(now, counts(20), "show")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportPopularity(ctx, popularityBatch(now.Add(-24*time.Hour), counts(15), "show")); err != nil {
		t.Fatal(err)
	}
	current, _ := popularityOf(t, s, "show")
	if *current.AllTime != 20 || !current.Fresh {
		t.Fatalf("an older batch replaced newer counts: %+v", current)
	}
}

func TestPopularityIsIndependentOfCorrectionsAndReimport(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	importAll(t, s, "show")
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.ImportPopularity(ctx, popularityBatch(now, `{"show":{"have_data":true,"all_time":5,"last_30day":1,"last_7day":0}}`, "show")); err != nil {
		t.Fatal(err)
	}
	before, err := s.Detail(ctx, "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(before["metadata"]), "all_time") {
		t.Fatal("popularity leaked into recording metadata")
	}
	if err := s.Edit(ctx, "show", map[string]any{"venue": "Corrected"}, "Fix venue"); err != nil {
		t.Fatal(err)
	}
	importAll(t, s, "show")
	kept, _ := popularityOf(t, s, "show")
	if *kept.AllTime != 5 {
		t.Fatalf("correction or re-import touched counts: %+v", kept)
	}
	var history int
	if err := s.Core.Pool.QueryRow(ctx, `SELECT count(*) FROM source_catalog_history WHERE item_id='show'`).Scan(&history); err != nil || history != 1 {
		t.Fatalf("popularity import wrote editorial history: %d %v", history, err)
	}
	var venue string
	if err := s.Core.Pool.QueryRow(ctx, `SELECT effective->>'venue' FROM source_catalog_items WHERE identifier='show'`).Scan(&venue); err != nil || venue != "Corrected" {
		t.Fatalf("correction lost: %s %v", venue, err)
	}
}

func TestPopularityBatchValidation(t *testing.T) {
	now := time.Now()
	good := popularityBatch(now, `{}`, "show")
	if err := good.validate(now); err != nil {
		t.Fatal(err)
	}
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("id%02d", i)
	}
	for name, mutate := range map[string]func(*PopularityBatch){
		"provider":     func(b *PopularityBatch) { b.Provider = "Archive Views" },
		"none":         func(b *PopularityBatch) { b.Requested = nil },
		"too many":     func(b *PopularityBatch) { b.Requested = many },
		"bad id":       func(b *PopularityBatch) { b.Requested = []string{"../x"} },
		"no time":      func(b *PopularityBatch) { b.ObservedAt = time.Time{} },
		"future":       func(b *PopularityBatch) { b.ObservedAt = now.Add(time.Hour) },
		"response cap": func(b *PopularityBatch) { b.Response = make([]byte, popularityResponseLimit+1) },
	} {
		batch := good
		mutate(&batch)
		if err := batch.validate(now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDecodePopularityBatchKeepsExactBytes(t *testing.T) {
	exact := "{not-valid-json \x00 bytes"
	encoded, _ := json.Marshal(map[string]any{"provider": "archive_views", "observed_at": "2026-11-09T12:00:00Z", "requested": []string{"show"}, "response_base64": base64.StdEncoding.EncodeToString([]byte(exact))})
	batch, err := DecodePopularityBatch(strings.NewReader(string(encoded)))
	if err != nil || string(batch.Response) != exact {
		t.Fatalf("%q %v", batch.Response, err)
	}
	if _, err := DecodePopularityBatch(strings.NewReader(`{"provider":"a","unexpected":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := DecodePopularityBatch(strings.NewReader(`{} {}`)); err == nil {
		t.Fatal("trailing batch accepted")
	}
}
