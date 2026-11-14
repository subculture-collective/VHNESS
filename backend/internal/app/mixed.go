package app

import (
	"net/http"
	"strconv"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

func (s *Server) mixedCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := 1
	if q.Has("page") {
		var err error
		page, err = strconv.Atoi(q.Get("page"))
		if err != nil || page < 1 || page > 100000 {
			write(w, 400, map[string]string{"error": "invalid page"})
			return
		}
	}
	media := domain.Media(q.Get("media"))
	if (media != "" && !media.Valid()) || len(q.Get("q")) > 1000 || catalog.ValidateCatalogFilters(q.Get("creator"), q.Get("date_from"), q.Get("date_to")) != nil {
		write(w, 400, map[string]string{"error": "invalid catalog filter"})
		return
	}
	scope := []string(s.collectionScope())
	if len(q.Get("text")) > 200 || (q.Get("label") != "" && !domain.ValidLabelID(q.Get("label"))) {
		write(w, 400, map[string]string{"error": "invalid catalog filter"})
		return
	}
	result, err := s.DB.Catalog(r.Context(), catalog.CatalogQuery{Scope: scope, Collection: q.Get("collection"), Media: media, Search: q.Get("q"), ID: r.PathValue("id"), Page: page, Creator: q.Get("creator"), DateFrom: q.Get("date_from"), DateTo: q.Get("date_to"), Label: q.Get("label"), Text: q.Get("text")})
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if r.PathValue("id") != "" {
		if len(result.Items) != 1 {
			write(w, 404, map[string]string{"error": "catalog item unavailable"})
			return
		}
		write(w, 200, result.Items[0])
		return
	}
	write(w, 200, result)
}

func (s *Server) mixedCollections(w http.ResponseWriter, r *http.Request) {
	collections, err := s.DB.CatalogCollections(r.Context(), s.channelScope(r))
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog collections unavailable"})
		return
	}
	write(w, 200, collections)
}

func (s *Server) mixedLabels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	collection := q.Get("collection")
	if collection != "" && !s.allowsCollection(collection) {
		write(w, 404, map[string]string{"error": "collection unavailable"})
		return
	}
	labels, err := s.DB.CatalogLabels(r.Context(), s.collectionScope(), collection, q.Get("kind"))
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid label query"})
		return
	}
	write(w, 200, labels)
}

func (s *Server) mixedCreators(w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Query().Get("collection")
	if collection != "" && !s.allowsCollection(collection) {
		write(w, 404, map[string]string{"error": "collection unavailable"})
		return
	}
	creators, err := s.DB.CatalogCreators(r.Context(), s.collectionScope(), collection)
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	write(w, 200, creators)
}
