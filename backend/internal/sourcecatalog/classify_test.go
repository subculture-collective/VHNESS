package sourcecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

func recordedFixture(id, source, lineage, filesJSON string) []byte {
	if filesJSON == "" {
		filesJSON = `[{"name":"song.flac"}]`
	}
	src, _ := json.Marshal(source)
	lin, _ := json.Marshal(lineage)
	return []byte(fmt.Sprintf(`{"metadata":{"identifier":%q,"collection":["etree"],"creator":"Band","title":"Show","venue":"Room","date":"1977-05-08","source":%s,"lineage":%s},"files":%s}`, id, src, lin, filesJSON))
}

func sourceTypeOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	detail, err := s.Detail(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return detail["source_type"].(string)
}

func TestSourceTypeClassification(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	cases := []struct{ name, source, lineage, want string }{
		{"soundboard abbreviation", "SBD > Reel > DAT", "", "soundboard"},
		{"soundboard word", "Soundboard recording", "", "soundboard"},
		{"board tape", "Board tape from the mixer", "", "soundboard"},
		{"audience", "AUD: Schoeps CMC64 > Nakamichi", "", "audience"},
		{"audience word", "Audience recording", "", "audience"},
		{"matrix", "Matrix", "", "matrix"},
		{"matrix naming parts", "Matrix of SBD and AUD", "", "matrix"},
		{"matrix across fields", "Matrix", "SBD + AUD > DAT", "matrix"},
		{"fm", "FM broadcast > cassette", "", "fm_radio"},
		{"radio", "Radio broadcast", "", "fm_radio"},
		{"other webcast", "Webcast capture", "", "other"},
		{"other tv", "TV broadcast audio", "", "other"},
		{"other studio", "Studio session master", "", "other"},
		{"lineage alone", "", "SBD > DAT > CD", "soundboard"},
		{"agreeing fields", "SBD", "SBD > DAT", "soundboard"},
		{"no evidence", "", "", "unknown"},
		{"microphones are not audience evidence", "Schoeps CMC6 > Nakamichi RX-505", "", "unknown"},
		{"lossless format is not soundboard evidence", "FLAC 24/96 master", "FLAC > WAV", "unknown"},
		{"sound quality is not evidence", "Excellent quality, very clean", "", "unknown"},
		{"named unknown stays unknown", "Unknown (from the cloud files)", "", "unknown"},
		{"two types without matrix conflict", "SBD and AUD", "", "unknown"},
		{"cross-field conflict", "SBD", "AUD > DAT", "unknown"},
		{"matrix with an unrelated type conflicts", "Matrix", "FM > DAT", "unknown"},
		{"hedged", "Maybe SBD", "", "unknown"},
		{"questioned", "SBD?", "", "unknown"},
		{"negated", "Not a soundboard", "", "unknown"},
		{"incidental audience noise", "Audience noise throughout, Nakamichi deck", "", "unknown"},
		{"pre-fm is not a broadcast", "Pre-FM master", "", "unknown"},
		{"pre-fm keeps a stated board", "Pre-FM SBD", "", "soundboard"},
	}
	for i, c := range cases {
		id := fmt.Sprintf("class%02d", i)
		if err := s.Import(ctx, id, recordedFixture(id, c.source, c.lineage, ""), 200, "", ""); err != nil {
			t.Fatal(c.name, err)
		}
		if got := sourceTypeOf(t, s, id); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	if err := s.Import(ctx, "fileonly", recordedFixture("fileonly", "", "", `[{"name":"a.flac","source":"sbd"},{"name":"b.mp3","source":"derivative"}]`), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := sourceTypeOf(t, s, "fileonly"); got != "unknown" {
		t.Errorf("file inventory source value classified as %s", got)
	}
}

func TestSourceTypeSharedProjectionFilterAndVisibility(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i := 0; i < 105; i++ {
		id := fmt.Sprintf("board%03d", i)
		if err := s.Import(ctx, id, recordedFixture(id, "SBD", "", ""), 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"crowd1", "crowd2", "hidden"} {
		if err := s.Import(ctx, id, recordedFixture(id, "AUD", "", ""), 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	listed := false
	if err := s.SetListing(ctx, ListingDecision{Identifiers: []string{"hidden"}, Listed: &listed}, "Exclude fixture"); err != nil {
		t.Fatal(err)
	}
	audience, err := s.Search(ctx, Query{SourceType: "audience"})
	if err != nil || audience.Total != 2 || len(audience.Items) != 2 {
		t.Fatal(audience.Total, err)
	}
	if audience.Items[0]["source_type"] != "audience" {
		t.Fatalf("list projection %#v", audience.Items[0])
	}
	if got := sourceTypeOf(t, s, audience.Items[0]["identifier"].(string)); got != "audience" {
		t.Fatalf("detail projection %s", got)
	}
	sample, err := s.Sample(ctx, SampleQuery{Seed: "seed", Limit: 10})
	if err != nil || sample.Items[0]["source_type"] == nil {
		t.Fatalf("sample projection %#v %v", sample.Items, err)
	}
	first, err := s.Search(ctx, Query{SourceType: "soundboard"})
	if err != nil || first.Total != 105 || len(first.Items) != 100 || first.Cursor == "" {
		t.Fatal(first.Total, len(first.Items), err)
	}
	second, err := s.Search(ctx, Query{SourceType: "soundboard", Cursor: first.Cursor})
	if err != nil || len(second.Items) != 5 || second.Total != 105 {
		t.Fatal(len(second.Items), err)
	}
	if _, err := s.Search(ctx, Query{SourceType: "audience", Cursor: first.Cursor}); err == nil {
		t.Fatal("a cursor must not survive a changed source type")
	}
	if _, err := s.Search(ctx, Query{SourceType: "tape"}); err == nil {
		t.Fatal("invalid source type accepted")
	}
	if err := s.Core.Collection(ctx, domain.Collection{ID: "other", Policy: domain.Stream}); err != nil {
		t.Fatal(err)
	}
	other := &Store{Core: s.Core, Collection: "other"}
	if err := other.Init(ctx); err != nil {
		t.Fatal(err)
	}
	foreign, err := other.Search(ctx, Query{SourceType: "soundboard"})
	if err != nil || foreign.Total != 0 {
		t.Fatal("collection leaked", foreign.Total, err)
	}
}

func TestSourceTypeFollowsCorrectionsAndReimport(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	if err := s.Import(ctx, "show", recordedFixture("show", "AUD", "", ""), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Edit(ctx, "show", map[string]any{"source": "SBD"}, "Taper confirmed a board feed"); err != nil {
		t.Fatal(err)
	}
	if got := sourceTypeOf(t, s, "show"); got != "soundboard" {
		t.Fatalf("correction not recomputed: %s", got)
	}
	if err := s.Import(ctx, "show", recordedFixture("show", "FM broadcast", "", ""), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := sourceTypeOf(t, s, "show"); got != "soundboard" {
		t.Fatalf("re-import lost the source correction: %s", got)
	}
	if err := s.Edit(ctx, "show", map[string]any{"source": nil}, "Withdraw correction"); err != nil {
		t.Fatal(err)
	}
	if got := sourceTypeOf(t, s, "show"); got != "fm_radio" {
		t.Fatalf("restored source not recomputed: %s", got)
	}
	if err := s.Import(ctx, "show", recordedFixture("show", "SBD", "AUD", ""), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := sourceTypeOf(t, s, "show"); got != "unknown" {
		t.Fatalf("conflict after re-import: %s", got)
	}
	if err := s.Edit(ctx, "show", map[string]any{"source_type": "matrix"}, "Reviewed against the tape notes"); err != nil {
		t.Fatal(err)
	}
	if got := sourceTypeOf(t, s, "show"); got != "matrix" {
		t.Fatalf("reviewed classification ignored: %s", got)
	}
	if err := s.Import(ctx, "show", recordedFixture("show", "AUD", "", ""), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := sourceTypeOf(t, s, "show"); got != "matrix" {
		t.Fatalf("re-import overrode a reviewed classification: %s", got)
	}
	if err := s.Edit(ctx, "show", map[string]any{"source_type": "tape"}, "Bad"); err == nil {
		t.Fatal("unknown editorial source type accepted")
	}
}

func TestSourceTypeBackfillIsBoundedAndResumable(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("legacy%d", i)
		if err := s.Import(ctx, id, recordedFixture(id, "SBD", "", ""), 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`ALTER TABLE source_catalog_items DISABLE TRIGGER source_catalog_items_projection`,
		`UPDATE source_catalog_items SET source_type='unknown',source_type_version=0,source_type_evidence='{}'`,
		`ALTER TABLE source_catalog_items ENABLE TRIGGER source_catalog_items_projection`,
	} {
		if _, err := s.Core.Pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.Search(ctx, Query{SourceType: "unknown"})
	if err != nil || pending.Total != 5 {
		t.Fatal("pending records must read as unknown", pending.Total, err)
	}
	detail, err := s.Detail(ctx, "legacy0")
	if err != nil || !detail["source_classification"].(map[string]any)["pending"].(bool) {
		t.Fatalf("pending state not reported: %#v %v", detail["source_classification"], err)
	}
	classified, remaining, err := s.ClassifyBatch(ctx, 2)
	if err != nil || classified != 2 || remaining != 3 {
		t.Fatal(classified, remaining, err)
	}
	board, err := s.Search(ctx, Query{SourceType: "soundboard"})
	if err != nil || board.Total != 2 {
		t.Fatal("partial backfill", board.Total, err)
	}
	unknown, err := s.Search(ctx, Query{SourceType: "unknown"})
	if err != nil || unknown.Total != 3 {
		t.Fatal("restart state", unknown.Total, err)
	}
	for remaining > 0 {
		if _, remaining, err = s.ClassifyBatch(ctx, 2); err != nil {
			t.Fatal(err)
		}
	}
	board, err = s.Search(ctx, Query{SourceType: "soundboard"})
	if err != nil || board.Total != 5 {
		t.Fatal("completed backfill", board.Total, err)
	}
	classified, remaining, err = s.ClassifyBatch(ctx, 2)
	if err != nil || classified != 0 || remaining != 0 {
		t.Fatal("a finished backfill must do nothing", classified, remaining, err)
	}
}

func TestOldClientCursorFingerprintUnchanged(t *testing.T) {
	type priorQuery struct{ Search, Year, Artist, Venue, Availability, Cursor, From, To, Collection, MonthDay, YearFrom, YearTo, Sort, Month string }
	prior, _ := json.Marshal(priorQuery{Artist: "Band", Sort: "date_desc", Collection: "etree"})
	if got, want := (Query{Artist: "Band", Sort: "date_desc", Collection: "etree"}).fingerprint(), fmt.Sprintf("%x", sha256.Sum256(prior)); got != want {
		t.Fatalf("existing cursors would be invalidated: %s != %s", got, want)
	}
}

func TestSourceTypeValidation(t *testing.T) {
	for value, ok := range map[string]bool{"": true, "soundboard": true, "audience": true, "matrix": true, "fm_radio": true, "other": true, "unknown": true, "FM": false, "sbd": false} {
		if err := (Query{SourceType: value}).Validate(); (err == nil) != ok {
			t.Errorf("%q: %v", value, err)
		}
	}
}
