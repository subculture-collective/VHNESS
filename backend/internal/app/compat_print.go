package app

import (
	"encoding/json"
	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/preservation"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Server) legacyPrintCollections(w http.ResponseWriter, r *http.Request) {
	c := s.configuredCollection()
	_, total, err := s.DB.LegacyPrintDocuments(r.Context(), c, "", "", "", "", 1, 0)
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if s.DB.PublicOnly && total == 0 {
		write(w, 200, []json.RawMessage{})
		return
	}
	collection, err := s.DB.LegacyPrintCollection(r.Context(), c)
	if err != nil {
		write(w, 503, map[string]string{"error": "collection configuration unavailable"})
		return
	}
	write(w, 200, []json.RawMessage{collection})
}
func (s *Server) legacyDocuments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := 20, 0
	if q.Has("limit") {
		var err error
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid limit"})
			return
		}
	}
	if q.Has("offset") {
		var err error
		offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid offset"})
			return
		}
	}
	if limit < 1 || limit > 50 || offset < 0 || offset > 100000 || len(q.Get("q")) > 200 || len(q.Get("subject")) > 100 || len(q.Get("language")) > 50 {
		write(w, 400, map[string]string{"error": "invalid document filter"})
		return
	}
	filter := catalog.PrintBrowseFilters{Creator: q.Get("creator"), DateFrom: q.Get("date_from"), DateTo: q.Get("date_to"), Series: q.Get("series")}
	if err := filter.Validate(); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	docs, total, err := s.DB.LegacyPrintDocuments(r.Context(), s.configuredCollection(), r.PathValue("id"), q.Get("q"), q.Get("subject"), q.Get("language"), limit, offset, filter)
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if r.PathValue("id") != "" {
		if len(docs) != 1 {
			write(w, 404, map[string]string{"error": "document unavailable"})
			return
		}
		write(w, 200, docs[0])
		return
	}
	write(w, 200, map[string]any{"documents": docs, "total": total, "limit": limit, "offset": offset})
}
func (s *Server) legacyFacets(w http.ResponseWriter, r *http.Request) {
	facets, err := s.DB.LegacyPrintFacets(r.Context(), s.configuredCollection())
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	series, err := s.DB.LegacyPrintSeries(r.Context(), s.configuredCollection())
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	write(w, 200, map[string]any{"subjects": facets, "series": series})
}
func (s *Server) legacyPrintMapping(r *http.Request) bool {
	var item, rep string
	err := s.DB.Pool.QueryRow(r.Context(), `SELECT min(item_id),min(representation_id) FROM `+s.DB.CatalogView()+` WHERE collection_id=$1 AND media='print' AND representation_id=$2 HAVING count(DISTINCT (item_id,representation_id))=1`, s.configuredCollection(), r.PathValue("id")).Scan(&item, &rep)
	if err != nil {
		return false
	}
	r.SetPathValue("id", item)
	r.SetPathValue("representation", rep)
	return true
}
func (s *Server) legacyPrintAsset(w http.ResponseWriter, r *http.Request) {
	if !s.legacyPrintMapping(r) {
		write(w, 404, map[string]string{"error": "asset unavailable"})
		return
	}
	q := r.URL.Query()
	q.Del("file")
	q.Del("action")
	q.Set("action", string(domain.ReadAction))
	if q.Get("download") == "1" {
		q.Set("action", string(domain.DownloadAction))
	}
	r.URL.RawQuery = q.Encode()
	e, action, file, err := s.authorizedTransport(r)
	if err != nil || e.Collection.Policy != domain.Local || s.Media == "" {
		write(w, 404, map[string]string{"error": "asset unavailable"})
		return
	}
	var original domain.LocalOriginal
	for _, asset := range e.Preservation.Manifest {
		if asset.File == file {
			original = asset
			break
		}
	}
	f, err := preservation.Verify(s.Media, original.Path, original.SHA256, original.Bytes)
	if err != nil {
		write(w, 503, map[string]string{"error": "local original failed integrity check"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		write(w, 503, map[string]string{"error": "local original unavailable"})
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+original.SHA256+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	disposition := "inline"
	if action == domain.DownloadAction {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(file)}))
	http.ServeContent(w, r, file, info.ModTime(), f)
}
func (s *Server) legacyPrintCover(w http.ResponseWriter, r *http.Request) {
	if !s.legacyPrintMapping(r) {
		write(w, 404, map[string]string{"error": "cover unavailable"})
		return
	}
	q := r.URL.Query()
	q.Del("file")
	q.Set("action", string(domain.ReadAction))
	r.URL.RawQuery = q.Encode()
	s.printCover(w, r)
}
func (s *Server) legacyPrintSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if len(query) > 200 || strings.TrimSpace(query) == "" {
		write(w, 400, map[string]string{"error": "search query required within bounds"})
		return
	}
	if !s.legacyPrintMapping(r) {
		write(w, 404, map[string]string{"error": "document unavailable"})
		return
	}
	q := r.URL.Query()
	q.Del("file")
	q.Set("action", string(domain.ReadAction))
	r.URL.RawQuery = q.Encode()
	_, file, err := s.authorizedPrint(r)
	if err != nil {
		write(w, 404, map[string]string{"error": "document text unavailable"})
		return
	}
	hits, err := s.DB.PrintSearch(r.Context(), r.PathValue("id"), r.PathValue("representation"), file, query)
	if err != nil {
		write(w, 503, map[string]string{"error": "document search unavailable"})
		return
	}
	out := []map[string]any{}
	for _, hit := range hits {
		out = append(out, map[string]any{"variant": r.PathValue("representation"), "page": hit.Page, "snippet": hit.Snippet})
	}
	write(w, 200, out)
}
