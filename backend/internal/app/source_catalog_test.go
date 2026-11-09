package app

import (
	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceCatalogIsScopedAndPrivate(t *testing.T) {
	for _, path := range []string{"/api/source-catalog/v1/items", "/api/source-catalog/v1/items/source", "/api/source-catalog/v1/status", "/api/source-catalog/v1/facets", "/api/source-catalog/v1/directory", "/api/source-catalog/v1/calendar", "/api/source-catalog/v1/artist", "/api/source-catalog/v1/artist-photos", "/api/source-catalog/v1/artist-media/" + strings.Repeat("a", 64)} {
		w := httptest.NewRecorder()
		(&Server{DB: &catalog.DB{PublicOnly: true}, Collection: "etree"}).Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("unreviewed public source inventory: %s %d", path, w.Code)
		}
		w = httptest.NewRecorder()
		(&Server{DB: &catalog.DB{}, Collection: "etree"}).Handler().ServeHTTP(w, httptest.NewRequest("GET", path+"?collection=other", nil))
		if w.Code != 404 {
			t.Fatalf("foreign source inventory: %s %d", path, w.Code)
		}
	}
}

func TestDirectoryRejectsInvalidHTTPQueries(t *testing.T) {
	for _, query := range []string{"dimension=bad", "dimension=artists&limit=0", "dimension=artists&limit=101", "dimension=years&cursor=bad", "dimension=venues&sort=bad"} {
		w := httptest.NewRecorder()
		(&Server{DB: &catalog.DB{}, Collection: "etree"}).Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/source-catalog/v1/directory?collection=etree&"+query, nil))
		if w.Code != 400 {
			t.Fatalf("invalid directory query: %s: %d", query, w.Code)
		}
	}
}
