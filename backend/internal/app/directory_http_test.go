package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/sourcecatalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDirectoryHTTPRoundTrip(t *testing.T) {
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
	schema := fmt.Sprintf("directory_http_%d", time.Now().UnixNano())
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
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/source-catalog/v1/directory?collection=etree&"+query, nil))
		return w
	}
	w := get("dimension=artists&sort=name_asc&limit=1")
	var first sourcecatalog.DirectoryPage
	if err = json.Unmarshal(w.Body.Bytes(), &first); err != nil || w.Code != 200 || first.Total != 2 || len(first.Items) != 1 || first.Items[0].Value != "Alpha" || first.Cursor == "" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	w = get("dimension=artists&sort=name_asc&limit=1&cursor=" + url.QueryEscape(first.Cursor))
	var second sourcecatalog.DirectoryPage
	if err = json.Unmarshal(w.Body.Bytes(), &second); err != nil || w.Code != 200 || second.Total != 2 || len(second.Items) != 1 || second.Items[0].Value != "Beta" || second.Cursor != "" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	w = get("dimension=venues&sort=name_asc&limit=1&cursor=" + url.QueryEscape(first.Cursor))
	if w.Code != 400 {
		t.Fatal("foreign dimension cursor accepted", w.Code)
	}
}
