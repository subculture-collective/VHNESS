package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/sourcecatalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSampleHTTPRoundTrip(t *testing.T) {
	address := os.Getenv("TEST_DATABASE_URL")
	if address == "" {
		t.Skip("explicitly disposable TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := catalog.Open(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Close()
	schema := fmt.Sprintf("sample_http_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	config, err := pgxpool.ParseConfig(address)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	d := &catalog.DB{Pool: pool}
	if err = d.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err = d.Collection(ctx, domain.Collection{ID: "etree", Policy: domain.Stream}); err != nil {
		t.Fatal(err)
	}
	store := &sourcecatalog.Store{Core: d, Collection: "etree"}
	if err = store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for i, artist := range []string{"Alpha", "Beta", "Excluded"} {
		id := fmt.Sprintf("http%02d", i)
		raw := []byte(fmt.Sprintf(`{"metadata":{"identifier":%q,"collection":["etree"],"creator":%q,"title":"Show","venue":"Room","date":"2003-09-23"},"files":[]}`, id, artist))
		if err = store.Import(ctx, id, raw, 200, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	listed := false
	if err = store.SetListing(ctx, sourcecatalog.ListingDecision{Identifiers: []string{"http02"}, Listed: &listed}, "Excluded HTTP fixture"); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{DB: d, Collection: "etree"}).Handler()
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/source-catalog/v1/sample?collection=etree&"+query, nil))
		return w
	}
	w := get("seed=stable-example&limit=3")
	var first sourcecatalog.SamplePage
	if err = json.Unmarshal(w.Body.Bytes(), &first); err != nil || w.Code != 200 || len(first.Items) != 2 || first.Seed != "stable-example" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	for _, item := range first.Items {
		if item["identifier"] == "http02" || item["creator"] == nil || item["unavailable"] == nil {
			t.Fatal(item)
		}
	}
	again := get("seed=stable-example&limit=3")
	if w.Body.String() != again.Body.String() {
		t.Fatal("same HTTP seed changed")
	}
	for _, key := range []string{"cursor", "total", "status"} {
		var raw map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &raw)
		if _, ok := raw[key]; ok {
			t.Fatal("unbounded field", key)
		}
	}
}
