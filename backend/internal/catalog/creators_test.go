package catalog

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Shared credits are split into people, and spellings that differ only in case
// are one person, on the native catalog and the print compatibility contract.
func TestCreatorFacetIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; creator facet unverified")
	}
	ctx := context.Background()
	admin, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Close()
	schema := "vhness_creators_test_" + time.Now().UTC().Format("20060102150405")
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
	for _, id := range []string{"zines", "other"} {
		if err = db.Collection(ctx, domain.Collection{ID: id, Policy: domain.Local, Budget: 1 << 20, Reserve: 1}); err != nil {
			t.Fatal(err)
		}
	}
	raw := []byte(`{"files":[{"name":"original.pdf"}]}`)
	if err = db.Snapshot(ctx, "creator-source", raw, 200, "", "", nil); err != nil {
		t.Fatal(err)
	}
	// Native order is newest first; print compatibility order is by title.
	fixtures := []struct {
		id, collection, title, date, creator string
		legacy                               map[string]any
	}{
		{"newest", "zines", "Beta", "2026-10-05", "kathleen hanna, Tobi Vail", nil},
		{"older", "zines", "Alpha", "2026-10-04", "Kathleen Hanna", nil},
		{"oldest", "zines", "Gamma", "2026-10-03", " Tobi Vail ,  , Allison Wolfe ", nil},
		{"uncredited", "zines", "Delta", "2026-10-02", "", nil},
		{"elsewhere", "other", "Epsilon", "2026-10-01", "Other Person", nil},
		{"unpublished", "zines", "Zeta", "2026-09-30", "Hidden Person", map[string]any{"published": false}},
		{"listed", "zines", "Eta", "2026-09-29", "Corin Tucker", map[string]any{"overrides": map[string]any{"creator": "Carrie Brownstein"}}},
		{"array", "zines", "Theta", "2026-09-28", "Array Person", map[string]any{"metadata": map[string]any{"title": "Theta", "creator": []string{"Array Person"}}}},
	}
	for _, f := range fixtures {
		legacy := map[string]any{"published": true, "metadata": map[string]any{"title": f.title, "creator": f.creator}, "overrides": map[string]any{}}
		for k, v := range f.legacy {
			legacy[k] = v
		}
		item := domain.CatalogItem{ID: f.id, Title: f.title, Creator: f.creator, Date: f.date, Collections: []string{f.collection},
			Representations: []domain.Representation{{ID: "pdf", Media: domain.PrintMedia, SourceItemID: "creator-source", Snapshot: domain.Digest(raw), File: "original.pdf", Collections: []string{f.collection}, Extension: domain.MediaExtension{Print: &domain.PrintExtension{DocumentID: f.id, Variant: "original", Pages: 1}}}}}
		item.Editorial, _ = json.Marshal(map[string]any{"legacy_document": legacy})
		if err = db.InsertCatalog(ctx, item, "Creator fixture"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(label string, got []CatalogCreator, err error, want []CatalogCreator) {
		t.Helper()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v (%v), want %v", label, got, err, want)
		}
	}
	got, err := db.CatalogCreators(ctx, nil, "")
	check("unrestricted", got, err, []CatalogCreator{{"Allison Wolfe", 1}, {"Array Person", 1}, {"Corin Tucker", 1}, {"Hidden Person", 1}, {"kathleen hanna", 2}, {"Other Person", 1}, {"Tobi Vail", 2}})
	got, err = db.CatalogCreators(ctx, nil, "other")
	check("one collection", got, err, []CatalogCreator{{"Other Person", 1}})
	got, err = db.CatalogCreators(ctx, domain.CollectionScope{"zines"}, "other")
	check("collection outside the scope", got, err, []CatalogCreator{})
	got, err = db.CatalogCreators(ctx, domain.CollectionScope{}, "")
	check("empty scope", got, err, []CatalogCreator{})

	got, err = db.LegacyPrintCreators(ctx, "zines")
	check("print compatibility", got, err, []CatalogCreator{{"Allison Wolfe", 1}, {"Carrie Brownstein", 1}, {"Kathleen Hanna", 2}, {"Tobi Vail", 2}})
	got, err = db.LegacyPrintCreators(ctx, "missing")
	check("print compatibility outside the collection", got, err, []CatalogCreator{})
}
