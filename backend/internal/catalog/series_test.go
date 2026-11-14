package catalog

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A series is assigned through the reviewed label workflow; the catalog and the
// print compatibility contract then list its issues in issue order.
func TestSeriesLabelsIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; series labels unverified")
	}
	ctx := context.Background()
	admin, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Close()
	schema := "vhness_series_test_" + time.Now().UTC().Format("20060102150405")
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE")
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db := &DB{Pool: pool}
	if err = db.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatal(err)
	}
	c := domain.Collection{ID: "series-fixture", Policy: domain.Local, Budget: 1 << 20, Reserve: 1}
	if err = db.Collection(ctx, c); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"files":[{"name":"original.pdf"}]}`)
	if err = db.Snapshot(ctx, "series-source", raw, 200, "", "", nil); err != nil {
		t.Fatal(err)
	}
	// The issue order below differs from both the date order and the title/ID order.
	for i, id := range []string{"issue-a", "issue-b", "issue-c", "issue-d"} {
		item := domain.CatalogItem{ID: id, Title: "Zine " + id, Date: "2026-10-0" + string(rune('1'+i)), Collections: []string{c.ID},
			Representations: []domain.Representation{{ID: "pdf", Media: domain.PrintMedia, SourceItemID: "series-source", Snapshot: domain.Digest(raw), File: "original.pdf", Collections: []string{c.ID}, Extension: domain.MediaExtension{Print: &domain.PrintExtension{DocumentID: id, Variant: "original", Pages: 1}}}}}
		item.Editorial, _ = json.Marshal(map[string]any{"legacy_document": map[string]any{"published": true, "metadata": map[string]any{"title": item.Title}, "overrides": map[string]any{}}})
		if err = db.InsertCatalog(ctx, item, "Series fixture"); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.ImportLabels(ctx, []domain.Label{{ID: "riot", Kind: domain.SeriesKind, Label: "Riot Grrrl"}, {ID: "strikes", Kind: "topic", Label: "Strikes"}}); err != nil {
		t.Fatal(err)
	}
	position := func(n int) *int { return &n }
	for _, invalid := range []domain.LabelProposal{
		{Item: "issue-a", Label: "riot", Method: "editor"},
		{Item: "issue-a", Label: "strikes", Method: "editor", IssuePosition: position(1), IssueLabel: "Volume 1"},
		{Item: "issue-a", Label: "riot", Method: "editor", IssuePosition: position(-1), IssueLabel: "Volume 1"},
		{Item: "issue-a", Label: "riot", Method: "editor", IssuePosition: position(1)},
		{Item: "issue-a", Label: "riot", Method: "editor", IssueLabel: "Volume 1"},
		{Item: "issue-a", Label: "missing", Method: "editor"},
	} {
		if _, err = db.ProposeLabels(ctx, []domain.LabelProposal{invalid}, "Invalid series proposal"); err == nil {
			t.Fatal("invalid series proposal accepted", invalid)
		}
	}
	proposals := []domain.LabelProposal{
		{Item: "issue-b", Label: "riot", Method: "editor", IssuePosition: position(1), IssueLabel: "Volume 1"},
		{Item: "issue-d", Label: "riot", Method: "editor", IssuePosition: position(2), IssueLabel: "42 (print)"},
		{Item: "issue-a", Label: "riot", Method: "editor", IssuePosition: position(3), IssueLabel: "Volume 3"},
		{Item: "issue-c", Label: "riot", Method: "editor", IssuePosition: position(4), IssueLabel: "Volume 4"},
		{Item: "issue-a", Label: "strikes", Method: "lexicon-v1"},
	}
	if n, err := db.ProposeLabels(ctx, proposals, "Synthetic series proposal"); err != nil || n != len(proposals) {
		t.Fatal("propose", n, err)
	}
	if page, err := db.Catalog(ctx, CatalogQuery{Page: 1, Label: "riot"}); err != nil || page.Total != 0 {
		t.Fatal("proposed series listed", page, err)
	}
	accept := []domain.LabelDecision{}
	for _, p := range proposals[:4] {
		accept = append(accept, domain.LabelDecision{Item: p.Item, Label: p.Label, State: "accepted"})
	}
	accept = append(accept, domain.LabelDecision{Item: "issue-a", Label: "strikes", State: "accepted"})
	if _, err = db.ReviewLabels(ctx, accept, "Synthetic series acceptance"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ReviewLabels(ctx, []domain.LabelDecision{{Item: "issue-c", Label: "riot", State: "rejected"}}, "Not this series"); err != nil {
		t.Fatal(err)
	}
	var history int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM catalog_label_history WHERE label_id='riot' AND issue_position IS NOT NULL AND issue_label IS NOT NULL`).Scan(&history); err != nil || history != 9 {
		t.Fatal("series history", history, err)
	}

	page, err := db.Catalog(ctx, CatalogQuery{Page: 1, Label: "riot"})
	if err != nil || page.Total != 3 {
		t.Fatal("series filter", page, err)
	}
	want := []struct {
		id, issue string
		position  int
	}{{"issue-b", "Volume 1", 1}, {"issue-d", "42 (print)", 2}, {"issue-a", "Volume 3", 3}}
	for i, w := range want {
		item := page.Items[i]
		var series *domain.ItemLabel
		for j := range item.Labels {
			if item.Labels[j].ID == "riot" {
				series = &item.Labels[j]
			}
		}
		if item.ID != w.id || series == nil || series.Kind != domain.SeriesKind || series.IssueLabel != w.issue || series.IssuePosition == nil || *series.IssuePosition != w.position {
			t.Fatal("series issue order", i, item)
		}
	}
	for _, l := range page.Items[2].Labels {
		if l.ID == "strikes" && (l.IssuePosition != nil || l.IssueLabel != "") {
			t.Fatal("topic label carries issue fields", l)
		}
	}
	if page, err = db.Catalog(ctx, CatalogQuery{Page: 1}); err != nil || page.Total != 4 || page.Items[0].ID != "issue-d" || page.Items[3].ID != "issue-a" {
		t.Fatal("unfiltered catalog order changed", page, err)
	}
	if page, err = db.Catalog(ctx, CatalogQuery{Page: 1, Label: "strikes"}); err != nil || page.Total != 1 {
		t.Fatal("topic filter", page, err)
	}

	docs, total, err := db.LegacyPrintDocuments(ctx, c.ID, "", "", "", "", 50, 0, PrintBrowseFilters{Series: "riot"})
	if err != nil || total != 3 || len(docs) != 3 {
		t.Fatal("compatibility series filter", total, err)
	}
	for i, w := range want {
		var doc struct {
			ID     string `json:"id"`
			Series []struct {
				Slug          string `json:"slug"`
				Name          string `json:"name"`
				IssuePosition int    `json:"issue_position"`
				IssueLabel    string `json:"issue_label"`
			} `json:"series"`
		}
		if err = json.Unmarshal(docs[i], &doc); err != nil || doc.ID != w.id || len(doc.Series) != 1 || doc.Series[0].Slug != "riot" || doc.Series[0].Name != "Riot Grrrl" || doc.Series[0].IssueLabel != w.issue || doc.Series[0].IssuePosition != w.position {
			t.Fatal("compatibility series order", i, string(docs[i]), err)
		}
	}
	if _, total, err = db.LegacyPrintDocuments(ctx, c.ID, "", "", "", "", 50, 0, PrintBrowseFilters{Series: "strikes"}); err != nil || total != 0 {
		t.Fatal("topic label accepted as a series", total, err)
	}
	if _, _, err = db.LegacyPrintDocuments(ctx, c.ID, "", "", "", "", 50, 0, PrintBrowseFilters{Series: "Not A Slug"}); err == nil {
		t.Fatal("invalid series filter accepted")
	}
	series, err := db.LegacyPrintSeries(ctx, c.ID)
	if err != nil || len(series) != 1 || series[0] != (PrintSeries{Slug: "riot", Name: "Riot Grrrl"}) {
		t.Fatal("compatibility series facets", series, err)
	}
	if series, err = db.LegacyPrintSeries(ctx, "other"); err != nil || len(series) != 0 {
		t.Fatal("series facets outside the collection", series, err)
	}
}
