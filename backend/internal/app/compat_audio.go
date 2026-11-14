package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	audiomodule "git.subcult.tv/subculture-collective/vhness/internal/audio"
	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/music"
)

// Audio compatibility adapts the retained Dedhed contract to the same scoped
// catalog and transport checks used by the native core. It owns no decisions.
func (s *Server) legacyAudioEntries(r *http.Request) (map[string]music.Entry, error) {
	collection, err := s.audioCollection(r)
	if err != nil {
		return nil, err
	}
	out := map[string]music.Entry{}
	for page := 1; page <= 100000; page++ {
		result, err := s.DB.Catalog(r.Context(), catalog.CatalogQuery{Scope: []string{s.configuredCollection()}, Collection: collection, Media: domain.AudioMedia, Page: page})
		if err != nil {
			return nil, err
		}
		for _, item := range result.Items {
			for _, rep := range item.Representations {
				e, a, err := s.audioEntry(r, item.ID, rep.ID, collection, domain.PlayAction)
				if err != nil || e.Representation.Extension.Audio == nil || e.Representation.Extension.Audio.Recording == nil {
					continue
				}
				recording := *e.Representation.Extension.Audio.Recording
				if out[recording.ID].Media != "" {
					return nil, fmt.Errorf("ambiguous recording compatibility mapping")
				}
				legacyReview := music.Review{Published: true, Playback: true, Relay: e.Review.Relay, Acquisition: e.Review.Acquisition, PrivateOnly: !e.Review.Public, Kind: recording.Kind}
				sourceHash := rep.Snapshot
				var retained []byte
				if err := s.DB.Pool.QueryRow(r.Context(), `SELECT editorial->'legacy_record' FROM catalog_items WHERE id=$1`, item.ID).Scan(&retained); err != nil {
					return nil, err
				}
				var donor struct {
					Review     music.Review `json:"review"`
					SourceHash string       `json:"source_hash"`
				}
				if len(retained) > 0 && string(retained) != "null" {
					if err := json.Unmarshal(retained, &donor); err != nil {
						return nil, err
					}
					legacyReview = donor.Review
					sourceHash = donor.SourceHash
				}
				if s.DB.PublicOnly {
					legacyReview.PrivateOnly = false
					legacyReview.Relay = true
					legacyReview.Acquisition = false
					legacyReview.Notes = "Approved noncommercial audience-recording playback and scheduled relay. Source warnings and credits remain with the recording."
				}
				out[recording.ID] = music.Entry{Recording: recording, Review: legacyReview, Media: a.Path, SHA256: a.SHA256, SourceHash: sourceHash}
			}
		}
		if page*24 >= result.Total {
			break
		}
	}
	return out, nil
}
func (s *Server) legacyRecordings(w http.ResponseWriter, r *http.Request) {
	entries, err := s.legacyAudioEntries(r)
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if id := r.PathValue("id"); id != "" {
		e, ok := entries[id]
		if !ok {
			write(w, 404, map[string]string{"error": "That tape isn't in the library."})
			return
		}
		write(w, 200, e)
		return
	}
	items, total, page, err := legacyRecordingPage(entries, r.URL.Query())
	if err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "page": page})
}

func legacyRecordingPage(entries map[string]music.Entry, query url.Values) ([]music.Entry, int, int, error) {
	q := strings.ToLower(strings.TrimSpace(query.Get("q")))
	year := query.Get("year")
	venue := strings.ToLower(strings.TrimSpace(query.Get("venue")))
	from, to := query.Get("date_from"), query.Get("date_to")
	if len(q) > 200 || len(venue) > 200 || year != "" && (len(year) != 4 || strings.Trim(year, "0123456789") != "") {
		return nil, 0, 0, fmt.Errorf("use a four-digit year and at most 200 search characters")
	}
	for _, value := range []string{from, to} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return nil, 0, 0, fmt.Errorf("use complete YYYY-MM-DD date bounds")
			}
		}
	}
	if from != "" && to != "" && from > to {
		return nil, 0, 0, fmt.Errorf("the original-date range is reversed")
	}
	page, _ := strconv.Atoi(query.Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		return nil, 0, 0, fmt.Errorf("that page is past the end of the shelf")
	}
	items := []music.Entry{}
	for _, e := range entries {
		m := e.Recording
		completeDate := len(m.Date) == 10
		if completeDate {
			_, err := time.Parse("2006-01-02", m.Date)
			completeDate = err == nil
		}
		text := m.Title + " " + m.Venue + " " + m.Location + " " + m.Date + " " + m.Artist + " " + m.Source + " " + m.Taper + " " + m.Transferer
		for _, track := range m.Tracks {
			text += " " + track.Title
		}
		if (q == "" || strings.Contains(strings.ToLower(text), q)) && (year == "" || strings.HasPrefix(m.Date, year)) && (venue == "" || strings.Contains(strings.ToLower(m.Venue), venue)) && (from == "" || completeDate && m.Date >= from) && (to == "" || completeDate && m.Date <= to) {
			items = append(items, e)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i].Recording, items[j].Recording
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.ID < b.ID
	})
	total := len(items)
	start := (page - 1) * 20
	if start > total {
		start = total
	}
	end := start + 20
	if end > total {
		end = total
	}
	return items[start:end], total, page, nil
}
func (s *Server) legacyAudioMapping(r *http.Request, id string) (string, string, error) {
	c, err := s.audioCollection(r)
	if err != nil {
		return "", "", err
	}
	var item, representation string
	err = s.DB.Pool.QueryRow(r.Context(), `SELECT min(item_id),min(representation_id) FROM `+s.DB.CatalogView()+` WHERE collection_id=$1 AND media='audio' AND extension->'audio'->>'recording_id'=$2 HAVING count(DISTINCT (item_id,representation_id))=1`, c, id).Scan(&item, &representation)
	return item, representation, err
}
func (s *Server) legacyAudioMedia(w http.ResponseWriter, r *http.Request) {
	item, rep, err := s.legacyAudioMapping(r, r.PathValue("id"))
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	c, err := s.audioCollection(r)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	e, a, err := s.audioEntry(r, item, rep, c, domain.PlayAction)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	f, err := audiomodule.Verify(&s.AudioFiles, s.Media, s.Derivatives, e, a, domain.PlayAction)
	if err != nil {
		write(w, 503, map[string]string{"error": "audio source unavailable"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		write(w, 503, map[string]string{"error": "audio source unavailable"})
		return
	}
	w.Header().Set("Content-Type", representationMIME(domain.AudioMedia, a.Path))
	w.Header().Set("ETag", `"`+a.SHA256+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if !s.DB.PublicOnly {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, a.Path, info.ModTime(), f)
}
func (s *Server) legacyStation(w http.ResponseWriter, r *http.Request) {
	c, err := s.audioCollection(r)
	if err != nil {
		write(w, 503, map[string]string{"error": "station unavailable"})
		return
	}
	schedule, err := s.DB.AudioSchedule(r.Context(), c)
	if err != nil || schedule.Validate() != nil {
		write(w, 503, map[string]string{"error": "station unavailable"})
		return
	}
	now := time.Now().UTC()
	if s.Clock != nil {
		now = s.Clock().UTC()
	}
	legacy := music.Schedule{Revision: schedule.Revision, Programs: []music.Program{}}
	entries := map[string]music.Entry{}
	for _, p := range schedule.Programs {
		e, a, err := s.audioEntry(r, p.Item, p.Representation, c, domain.ScheduleAction)
		if err != nil || p.Duration > a.Duration || e.Representation.Extension.Audio == nil || e.Representation.Extension.Audio.Recording == nil {
			write(w, 503, map[string]string{"error": "scheduled recording unavailable"})
			return
		}
		recording := *e.Representation.Extension.Audio.Recording
		entries[recording.ID] = music.Entry{Recording: recording, Review: music.Review{Published: true, Playback: true}, Media: a.Path, SHA256: a.SHA256}
		legacy.Programs = append(legacy.Programs, music.Program{RecordingID: recording.ID, Start: p.Start, Duration: p.Duration})
		offset := now.Sub(p.Start).Seconds()
		if offset >= 0 && offset < p.Duration {
			f, err := audiomodule.Verify(&s.AudioFiles, s.Media, s.Derivatives, e, a, domain.ScheduleAction)
			if err != nil {
				write(w, 503, map[string]string{"error": "current recording failed integrity check"})
				return
			}
			f.Close()
		}
	}
	state, err := legacy.At(now, entries)
	if err != nil {
		write(w, 503, map[string]string{"error": "The station is off the air right now. Try again, or play a set from the library."})
		return
	}
	write(w, 200, state)
}
