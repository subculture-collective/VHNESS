package app

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	audiomodule "git.subcult.tv/subculture-collective/vhness/internal/audio"
	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

func (s *Server) audioEntry(r *http.Request, item, representation, collection string, action domain.Action) (catalog.TransportEntry, domain.AudioAsset, error) {
	e, err := s.DB.Transport(r.Context(), item, representation, collection)
	if err != nil {
		return e, domain.AudioAsset{}, err
	}
	a, err := s.DB.AudioAsset(r.Context(), item, representation, collection)
	if err != nil {
		return e, a, err
	}
	err = domain.AuthorizeAudioAsset(e.Collection, e.Representation, e.Raw, e.Review, e.Preservation, a, action)
	return e, a, err
}
func (s *Server) audioCollection(r *http.Request) (string, error) {
	c := r.URL.Query().Get("collection")
	if c == "" {
		c = s.configuredCollection()
	}
	if !s.allowsCollection(c) {
		return "", fmt.Errorf("audio collection unavailable")
	}
	return c, nil
}
func audioPlayback(item, representation, collection string, e catalog.TransportEntry, a domain.AudioAsset) map[string]any {
	q := url.Values{"collection": {collection}}
	// AuthorizeAudioAsset has already validated the retained snapshot.
	provider, _ := domain.ParseSourceIdentity(e.Representation.SourceItemID, e.Raw)
	return map[string]any{"recording": e.Representation.Extension.Audio.Recording, "policy": domain.Local, "url": "/api/catalog/v1/items/" + url.PathEscape(item) + "/representations/" + url.PathEscape(representation) + "/audio/media?" + q.Encode(), "source_url": provider.URL, "source_provider": provider.Provider, "snapshot_sha256": a.Snapshot, "sha256": a.SHA256, "duration": a.Duration}
}
func (s *Server) audioResolve(w http.ResponseWriter, r *http.Request) {
	c, err := s.audioCollection(r)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	item, representation := r.PathValue("id"), r.PathValue("representation")
	e, a, err := s.audioEntry(r, item, representation, c, domain.PlayAction)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	f, err := audiomodule.Verify(&s.AudioFiles, s.Media, s.Derivatives, e, a, domain.PlayAction)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	f.Close()
	write(w, 200, audioPlayback(item, representation, c, e, a))
}
func (s *Server) audioMedia(w http.ResponseWriter, r *http.Request) {
	c, err := s.audioCollection(r)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	e, a, err := s.audioEntry(r, r.PathValue("id"), r.PathValue("representation"), c, domain.PlayAction)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	f, err := audiomodule.Verify(&s.AudioFiles, s.Media, s.Derivatives, e, a, domain.PlayAction)
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		write(w, 404, map[string]string{"error": "audio unavailable"})
		return
	}
	w.Header().Set("Content-Type", representationMIME(domain.AudioMedia, a.Path))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"sha256-`+a.SHA256+`"`)
	http.ServeContent(w, r, "recording", info.ModTime(), f)
}
func (s *Server) audioChannel(w http.ResponseWriter, r *http.Request) {
	c, err := s.audioCollection(r)
	if err != nil {
		write(w, 404, map[string]string{"error": "station unavailable"})
		return
	}
	now := time.Now().UTC()
	if s.Clock != nil {
		now = s.Clock().UTC()
	}
	unavailable := func(reason string) {
		write(w, 200, map[string]any{"server_time": now, "state": "unavailable", "reason": reason})
	}
	schedule, err := s.DB.AudioSchedule(r.Context(), c)
	if err != nil || schedule.Validate() != nil || schedule.Collection != c {
		unavailable("station has no valid schedule")
		return
	}
	// All programs, including future entries, remain bound to current rights.
	var current int = -1
	var entry catalog.TransportEntry
	var asset domain.AudioAsset
	for i, p := range schedule.Programs {
		e, a, err := s.audioEntry(r, p.Item, p.Representation, c, domain.ScheduleAction)
		if err != nil || p.Duration > a.Duration {
			unavailable("scheduled recording unavailable")
			return
		}
		offset := now.Sub(p.Start).Seconds()
		if offset >= 0 && offset < p.Duration {
			current, entry, asset = i, e, a
		}
	}
	if current < 0 {
		unavailable("station has no current program")
		return
	}
	f, err := audiomodule.Verify(&s.AudioFiles, s.Media, s.Derivatives, entry, asset, domain.ScheduleAction)
	if err != nil {
		unavailable("current recording failed integrity check")
		return
	}
	f.Close()
	p := schedule.Programs[current]
	response := map[string]any{"server_time": now, "state": "playing", "revision": schedule.Revision, "program": p, "offset": now.Sub(p.Start).Seconds(), "playback": audioPlayback(p.Item, p.Representation, c, entry, asset)}
	if current+1 < len(schedule.Programs) {
		response["next"] = schedule.Programs[current+1]
	}
	write(w, 200, response)
}
