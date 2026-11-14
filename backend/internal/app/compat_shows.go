// Show-index adapter retained from Dedhed (GPL-3.0-only).
package app

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/music"
)

// Show-index endpoints read the collection-wide inventory. They describe what
// exists at the source; only entries that also pass eligible() can be played.

func (s *Server) legacyCalendar(w http.ResponseWriter, r *http.Request) {
	years, err := s.DB.LegacyYears(r.Context(), s.configuredCollection())
	if err != nil {
		slog.Error("calendar read failed", "error", err)
		write(w, 503, map[string]string{"error": "The show index isn't answering. Try again in a moment."})
		return
	}
	out := map[string]any{"years": years}
	if y := r.URL.Query().Get("year"); y != "" {
		year, err := strconv.Atoi(y)
		if err != nil {
			write(w, 400, map[string]string{"error": "That year isn't on the calendar."})
			return
		}
		days, err := s.DB.LegacyDays(r.Context(), s.configuredCollection(), year)
		if err != nil {
			status, message := legacyStatus(err, "That year isn't on the calendar.")
			write(w, status, map[string]string{"error": message})
			return
		}
		out["year"] = year
		out["days"] = days
	}
	write(w, 200, out)
}

// DateItem is one source item on a date, annotated with its library state.
type DateItem struct {
	music.SourceItem
	Playable bool   `json:"playable"`
	Library  string `json:"library"` // "", "imported", or "playable"
}

func (s *Server) legacyDate(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	items, err := s.DB.LegacyItemsOn(r.Context(), s.configuredCollection(), date)
	if err != nil {
		status, message := legacyStatus(err, "That isn't a date the calendar can read.")
		write(w, status, map[string]string{"error": message})
		return
	}
	entries, err := s.legacyAudioEntries(r)
	if err != nil {
		write(w, 503, map[string]string{"error": legacyCatalogUnavailable})
		return
	}
	out := []DateItem{}
	playable := 0
	for _, it := range items {
		d := DateItem{SourceItem: it}
		if e, ok := entries[it.ID]; ok {
			d.Library = "imported"
			if e.Review.Published && e.Review.Playback && e.Media != "" {
				d.Library = "playable"
				d.Playable = true
				playable++
			}
		}
		out = append(out, d)
	}
	// Playable recordings first; the inventory order (rating, downloads) is kept within each group.
	sorted := make([]DateItem, 0, len(out))
	for _, d := range out {
		if d.Playable {
			sorted = append(sorted, d)
		}
	}
	for _, d := range out {
		if !d.Playable {
			sorted = append(sorted, d)
		}
	}
	write(w, 200, map[string]any{"date": date, "items": sorted, "playable": playable})
}

func (s *Server) legacyOnThisDay(w http.ResponseWriter, r *http.Request) {
	now := time.Now
	if s.Clock != nil {
		now = s.Clock
	}
	t := now().UTC()
	month, day := int(t.Month()), t.Day()
	if m := r.URL.Query().Get("month"); m != "" {
		month, _ = strconv.Atoi(m)
	}
	if d := r.URL.Query().Get("day"); d != "" {
		day, _ = strconv.Atoi(d)
	}
	days, err := s.DB.LegacyOnThisDay(r.Context(), s.configuredCollection(), month, day)
	if err != nil {
		status, message := legacyStatus(err, "invalid month or day")
		write(w, status, map[string]string{"error": message})
		return
	}
	write(w, 200, map[string]any{"month": month, "day": day, "dates": days})
}

func (s *Server) legacyShows(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	hits, total, err := s.DB.LegacySearch(r.Context(), s.configuredCollection(), r.URL.Query().Get("q"), r.URL.Query().Get("year"), page, 20)
	if err != nil {
		status, message := legacyStatus(err, "invalid search")
		write(w, status, map[string]string{"error": message})
		return
	}
	if hits == nil {
		hits = []catalog.SearchHit{}
	}
	write(w, 200, map[string]any{"dates": hits, "total": total, "page": page})
}

// legacyStatus keeps the compatibility wording for rejected input and reports
// every other failure, including an expired request deadline, as unavailable.
func legacyStatus(err error, invalid string) (int, string) {
	if errors.Is(err, catalog.ErrInvalidInput) {
		return 400, invalid
	}
	slog.Error("legacy show read failed", "error", err)
	return 503, "The show index isn't answering. Try again in a moment."
}
