package app

import (
	"errors"
	"net/http"
	"strconv"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/sourcecatalog"
	"github.com/jackc/pgx/v5"
)

func (s *Server) sourceCatalogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/source-catalog/v1/items", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/items/{id}", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/items/{id}/source", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/facets", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/sample", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/directory", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/status", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/calendar", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/artist", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/artist-photos", s.sourceCatalog)
	mux.HandleFunc("GET /api/source-catalog/v1/artist-media/{sha256}", s.sourceCatalog)
}
func (s *Server) sourceCatalog(w http.ResponseWriter, r *http.Request) {
	// A source inventory is not an operator-reviewed public catalog. Public
	// consumers keep the existing representation/public-review API boundary.
	if s.DB.PublicOnly {
		write(w, 404, map[string]string{"error": "source inventory is private"})
		return
	}
	collection := r.URL.Query().Get("collection")
	if collection == "" {
		collection = s.defaultCollection()
	}
	if !domain.ValidID(collection) || !s.allowsCollection(collection) {
		write(w, 404, map[string]string{"error": "collection unavailable"})
		return
	}
	store := &sourcecatalog.Store{Core: s.DB, Collection: collection}
	failed := func(err error) {
		if errors.Is(err, pgx.ErrNoRows) {
			write(w, 404, map[string]string{"error": "source metadata is not indexed"})
		} else {
			write(w, 503, map[string]string{"error": "metadata catalog unavailable"})
		}
	}
	if r.Pattern == "GET /api/source-catalog/v1/artist-media/{sha256}" {
		id := r.PathValue("sha256")
		contentType, data, err := store.ArtistMedia(r.Context(), id)
		if err != nil {
			failed(err)
			return
		}
		// Renditions are content-addressed, so a stored hash never changes.
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+id+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		if r.Header.Get("If-None-Match") == `"`+id+`"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(data)
		return
	}
	switch r.URL.Path {
	case "/api/source-catalog/v1/sample":
		values := r.URL.Query()
		for key := range values {
			if key != "collection" && key != "seed" && key != "limit" {
				write(w, 400, map[string]string{"error": "unsupported sample parameter"})
				return
			}
		}
		limit := 0
		if values.Has("limit") {
			var err error
			limit, err = strconv.Atoi(values.Get("limit"))
			if err != nil || limit < 1 {
				write(w, 400, map[string]string{"error": "use a sample limit from 1 to 50"})
				return
			}
		}
		q := sourcecatalog.SampleQuery{Seed: values.Get("seed"), Limit: limit}
		if err := q.Validate(); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		page, err := store.Sample(r.Context(), q)
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, page)
		return
	case "/api/source-catalog/v1/directory":
		values := r.URL.Query()
		limit := 0
		if values.Has("limit") {
			var err error
			limit, err = strconv.Atoi(values.Get("limit"))
			if err != nil || limit < 1 {
				write(w, 400, map[string]string{"error": "use a directory limit from 1 to 100"})
				return
			}
		}
		q := sourcecatalog.DirectoryQuery{Collection: collection, Dimension: values.Get("dimension"), Search: values.Get("q"), Sort: values.Get("sort"), Cursor: values.Get("cursor"), Limit: limit}
		if err := q.Validate(); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		page, err := store.Directory(r.Context(), q)
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, page)
		return
	case "/api/source-catalog/v1/calendar":
		values := r.URL.Query()
		q := sourcecatalog.CalendarQuery{Month: values.Get("month"), YearFrom: values.Get("year_from"), YearTo: values.Get("year_to"), Artist: values.Get("artist")}
		if err := q.Validate(); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		value, err := store.Calendar(r.Context(), q)
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, value)
		return
	case "/api/source-catalog/v1/artist":
		artist := r.URL.Query().Get("artist")
		if err := sourcecatalog.ValidateArtist(artist); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		value, err := store.Artist(r.Context(), artist)
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, value)
		return
	case "/api/source-catalog/v1/artist-photos":
		value, err := store.ArtistPhotos(r.Context())
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, map[string]any{"artists": value})
		return
	case "/api/source-catalog/v1/status":
		value, err := store.Status(r.Context())
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, value)
		return
	case "/api/source-catalog/v1/facets":
		value, err := store.Facets(r.Context())
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, value)
		return
	}
	if id := r.PathValue("id"); id != "" {
		if !domain.ValidID(id) {
			write(w, 400, map[string]string{"error": "invalid source identity"})
			return
		}
		listed, err := store.IsListed(r.Context(), id)
		if err != nil {
			failed(err)
			return
		}
		if !listed {
			write(w, 404, map[string]string{"error": "source metadata is not listed"})
			return
		}
		if r.Pattern == "GET /api/source-catalog/v1/items/{id}/source" {
			raw, err := store.Raw(r.Context(), id)
			if err != nil {
				failed(err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(raw)
			return
		}
		value, err := store.Detail(r.Context(), id)
		if err != nil {
			failed(err)
			return
		}
		write(w, 200, value)
		return
	}
	values := r.URL.Query()
	q := sourcecatalog.Query{Collection: collection, Search: values.Get("q"), Year: values.Get("year"), Artist: values.Get("artist"), Venue: values.Get("venue"), Availability: values.Get("availability"), Cursor: values.Get("cursor"), From: values.Get("date_from"), To: values.Get("date_to"), MonthDay: values.Get("month_day"), YearFrom: values.Get("year_from"), YearTo: values.Get("year_to"), Sort: values.Get("sort"), Month: values.Get("month")}
	if err := q.Validate(); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err := sourcecatalog.ValidateCursor(q); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	value, err := store.Search(r.Context(), q)
	if err != nil {
		failed(err)
		return
	}
	write(w, 200, value)
}
