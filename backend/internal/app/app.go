package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/community"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/preservation"
	"git.subcult.tv/subculture-collective/vhness/internal/video"
	"github.com/jackc/pgx/v5"
)

type Server struct {
	Compatibility    string
	Version          string
	Revision         string
	DB               *catalog.DB
	Community        *community.Server
	Clock            func() time.Time
	Media            string
	Derivatives      string
	VideoDerivatives string
	VideoReceipts    string
	Web              string
	Collection       string
	Collections      domain.CollectionScope
	// SummaryTTL reuses episode listing summaries for this long; zero disables reuse.
	SummaryTTL        time.Duration
	failures          sync.Mutex
	resolutionFailure *ResolutionFailure
	summaryCache      summaryCache
	loadSummaries     func(context.Context) (map[string]domain.Entry, error)
}
type ResolutionFailure struct {
	ID     string    `json:"episode_id"`
	Mode   string    `json:"mode"`
	Reason string    `json:"reason"`
	Time   time.Time `json:"at"`
	Count  int64     `json:"count_since_start"`
}

func (s *Server) failure(r *http.Request, reason string) {
	s.failures.Lock()
	defer s.failures.Unlock()
	count := int64(1)
	if s.resolutionFailure != nil {
		count += s.resolutionFailure.Count
	}
	s.resolutionFailure = &ResolutionFailure{r.PathValue("id"), r.URL.Query().Get("mode"), reason, time.Now().UTC(), count}
}
func (s *Server) failureStatus() *ResolutionFailure {
	s.failures.Lock()
	defer s.failures.Unlock()
	if s.resolutionFailure == nil {
		return nil
	}
	copy := *s.resolutionFailure
	return &copy
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.sourceCatalogRoutes(mux)
	if s.Community != nil {
		s.Community.Allowed = s.allowsCollection
		s.Community.Available = s.communityAvailable
		s.Community.Current = s.communityCurrent
		s.Community.CurrentMedia = s.communityCurrentMedia
		s.Community.Clock = s.Clock
		s.Community.Register(mux)
	}
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		audience := "private"
		if s.DB != nil && s.DB.PublicOnly {
			audience = "public"
		}
		write(w, 200, map[string]string{"status": "ok", "revision": s.Revision, "audience": audience})
	})
	if s.Compatibility == "audio" {
		mux.HandleFunc("GET /api/recordings", s.legacyRecordings)
		mux.HandleFunc("GET /api/recordings/{id}", s.legacyRecordings)
		mux.HandleFunc("GET /api/audio/{id}", s.legacyAudioMedia)
		mux.HandleFunc("GET /api/station", s.legacyStation)
		mux.HandleFunc("GET /api/calendar", s.legacyCalendar)
		mux.HandleFunc("GET /api/dates/{date}", s.legacyDate)
		mux.HandleFunc("GET /api/on-this-day", s.legacyOnThisDay)
		mux.HandleFunc("GET /api/shows", s.legacyShows)
	}
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		audience := "private"
		if s.DB != nil && s.DB.PublicOnly {
			audience = "public"
		}
		write(w, 200, map[string]any{"api_version": "1", "core_version": s.Version, "revision": s.Revision, "audience": audience, "collection": s.Collection, "collections": s.collectionScope(), "modules": map[string]string{"catalog": "1", "print": "1", "audio": "1", "video": "1", "source_catalog": "1", "source_discovery": "1", "source_artist_photos": "1", "source_artist_media": "1", "source_classification": "1"}, "community": s.communityFeatures()})
	})
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := s.DB.Pool.Ping(r.Context()); err != nil {
			write(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		if s.Compatibility == "print" {
			write(w, 200, map[string]any{"status": "ok", "stage": "archive", "public": s.DB.PublicOnly, "revision": s.Revision})
			return
		}
		write(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/episodes", s.list)
	mux.HandleFunc("GET /api/catalog/v1/items", s.mixedCatalog)
	mux.HandleFunc("GET /api/catalog/v1/collections", s.mixedCollections)
	mux.HandleFunc("GET /api/catalog/v1/labels", s.mixedLabels)
	mux.HandleFunc("GET /api/catalog/v1/creators", s.mixedCreators)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}", s.mixedCatalog)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}/representations/{representation}/resolve", s.resolveRepresentation)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}/representations/{representation}/media", s.representationMedia)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}/representations/{representation}/print", s.printStatus)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}/representations/{representation}/search", s.printSearch)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}/representations/{representation}/cover", s.printCover)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}/representations/{representation}/audio/resolve", s.audioResolve)
	mux.HandleFunc("GET /api/catalog/v1/items/{id}/representations/{representation}/audio/media", s.audioMedia)
	mux.HandleFunc("GET /api/audio/channel", s.audioChannel)
	if s.Compatibility == "print" {
		mux.HandleFunc("GET /api/collections", s.legacyPrintCollections)
		mux.HandleFunc("GET /api/documents", s.legacyDocuments)
		mux.HandleFunc("GET /api/documents/{id}", s.legacyDocuments)
		mux.HandleFunc("GET /api/facets", s.legacyFacets)
		mux.HandleFunc("GET /api/assets/{id}", s.legacyPrintAsset)
		mux.HandleFunc("GET /api/assets/{id}/cover", s.legacyPrintCover)
		mux.HandleFunc("GET /api/assets/{id}/search", s.legacyPrintSearch)
	} else {
		mux.HandleFunc("GET /api/collections", func(w http.ResponseWriter, r *http.Request) {
			collections, err := s.DB.Collections(r.Context())
			if err != nil {
				write(w, 503, map[string]string{"error": "collections unavailable"})
				return
			}
			if s.collectionScope() != nil {
				selected := []catalog.CollectionSummary{}
				for _, collection := range collections {
					if s.allowsCollection(collection.ID) {
						selected = append(selected, collection)
					}
				}
				collections = selected
			}
			write(w, 200, collections)
		})
	}
	mux.HandleFunc("GET /api/episodes/{id}", s.episode)
	mux.HandleFunc("GET /api/playback/{id}", s.playback)
	mux.HandleFunc("GET /api/video-captions/{id}/{index}", s.videoCaption)
	mux.HandleFunc("GET /api/channel", s.channel)
	mux.HandleFunc("GET /api/media/{id}", s.media)
	mux.HandleFunc("GET /api/status", s.status)
	if s.Web != "" {
		files := http.FileServer(http.Dir(s.Web))
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				write(w, 404, map[string]string{"error": "unknown API route"})
				return
			}
			if r.URL.Path != "/" {
				if info, err := os.Stat(filepath.Join(s.Web, filepath.Clean(r.URL.Path))); err != nil || info.IsDir() {
					http.ServeFile(w, r, filepath.Join(s.Web, "index.html"))
					return
				}
			}
			files.ServeHTTP(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.DB != nil && s.DB.PublicOnly && r.URL.Path == "/api/status" {
			write(w, 404, map[string]string{"error": "unknown API route"})
			return
		}
		if selected := r.URL.Query().Get("collection"); selected != "" && !s.allowsCollection(selected) {
			write(w, 404, map[string]string{"error": "collection unavailable"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// A connected but unresponsive database must not hold readiness or
			// the single in-flight channel poll indefinitely. Local file serving
			// keeps its normal streaming lifetime; only DB work uses this context.
			budget := 3 * time.Second
			// Whole-corpus private metadata counts may exceed the readiness and
			// playback deadline. Keep that larger budget confined to aggregation.
			if r.URL.Path == "/api/source-catalog/v1/items" || r.URL.Path == "/api/source-catalog/v1/status" || r.URL.Path == "/api/source-catalog/v1/facets" || r.URL.Path == "/api/source-catalog/v1/calendar" || r.URL.Path == "/api/source-catalog/v1/artist" || r.URL.Path == "/api/source-catalog/v1/artist-photos" {
				budget = 10 * time.Second
			}
			ctx, cancel := context.WithTimeout(r.Context(), budget)
			defer cancel()
			r = r.WithContext(ctx)
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	filters, err := parseVideoFilters(r.URL.Query())
	if err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	wait, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), summaryColdWait)
	defer cancel()
	entries, err := s.summaries(wait)
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	list := []domain.Episode{}
	query := strings.ToLower(r.URL.Query().Get("q"))
	year := r.URL.Query().Get("year")
	for _, e := range entries {
		if !s.allowsRequestCollection(r, e.Episode.CollectionID) {
			continue
		}
		if selected := r.URL.Query().Get("collection"); selected != "" && e.Episode.CollectionID != selected {
			continue
		}
		if !e.Published() {
			continue
		}
		ep := e.ViewerEpisode(e.SourceTitle)
		if query != "" && !strings.Contains(strings.ToLower(ep.Title+" "+ep.Description), query) {
			continue
		}
		if year != "" && !strings.HasPrefix(ep.Date, year) {
			continue
		}
		if !filters.matches(ep) {
			continue
		}
		ep.Files = nil
		list = append(list, ep)
	}
	sort.Slice(list, func(i, j int) bool { return domain.BrowseLess(list[i], list[j]) })
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	total := len(list)
	start := (page - 1) * 24
	if start > total || start < 0 {
		start = total
	}
	end := start + 24
	if end > total {
		end = total
	}
	write(w, 200, map[string]any{"episodes": list[start:end], "total": total, "page": page})
}
func (s *Server) episode(w http.ResponseWriter, r *http.Request) {
	e, err := s.DB.Entry(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if err != nil || !e.Published() || !s.allowsRequestCollection(r, e.Episode.CollectionID) {
		write(w, 404, map[string]string{"error": "episode unavailable"})
		return
	}
	if s.DB.PublicOnly {
		write(w, 200, map[string]any{"episode": e.ViewerEpisode(e.SourceTitle), "policy": e.Policy, "source_hash": e.SourceHash, "unavailable": e.Unavailable})
	} else {
		files := e.Episode.Files
		e.Episode = e.ViewerEpisode(e.SourceTitle)
		e.Episode.Files = files // Retain the existing private operator inventory contract.
		write(w, 200, e)
	}
}
func (s *Server) playback(w http.ResponseWriter, r *http.Request) {
	e, err := s.DB.Entry(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if err != nil || !s.allowsRequestCollection(r, e.Episode.CollectionID) {
		s.failure(r, "episode unavailable")
		write(w, 404, map[string]string{"error": "episode unavailable"})
		return
	}
	p, err := domain.Resolve(e, r.URL.Query().Get("mode") == "channel")
	if err != nil {
		s.failure(r, err.Error())
		write(w, 404, map[string]string{"error": err.Error()})
		return
	}
	write(w, 200, p)
}
func (s *Server) channel(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock()
	}
	schedule, err := s.DB.Schedule(r.Context())
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			write(w, 503, map[string]string{"error": "schedule unavailable"})
			return
		}
		write(w, 200, domain.Now{ServerTime: now.UTC(), State: "unavailable", Reason: "channel has no schedule"})
		return
	}
	within, err := s.DB.ScheduleWithinScope(r.Context(), schedule, s.channelScope(r))
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if !within {
		write(w, 200, domain.Now{ServerTime: now.UTC(), State: "unavailable", Reason: "schedule contains a different collection"})
		return
	}
	entries, err := s.DB.CurrentEntries(r.Context(), schedule, now)
	if err != nil {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	state := schedule.At(now, entries)
	if state.Episode != nil {
		// Polling viewers need identity and attribution, not the parent item's
		// entire file inventory. Complete inventories remain in the catalog.
		e := entries[state.Episode.ID]
		ep := e.ViewerEpisode(e.SourceTitle)
		state.Episode = &ep
	}
	write(w, 200, state)
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("collection")
	if id == "" {
		id = s.defaultCollection()
	}
	if !s.allowsCollection(id) {
		write(w, 404, map[string]string{"error": "select an available collection"})
		return
	}
	state, err := s.DB.State(r.Context(), id)
	if err != nil {
		write(w, 503, map[string]string{"error": "worker status unavailable"})
		return
	}
	counts, err := s.DB.Counts(r.Context(), id)
	if err != nil {
		write(w, 503, map[string]string{"error": "queue status unavailable"})
		return
	}
	inventory, err := s.DB.Inventory(r.Context(), id)
	if err != nil {
		write(w, 503, map[string]string{"error": "inventory status unavailable"})
		return
	}
	collection, err := s.DB.GetCollection(r.Context(), id)
	if err != nil {
		write(w, 503, map[string]string{"error": "collection status unavailable"})
		return
	}
	now := time.Now().UTC()
	if s.Clock != nil {
		now = s.Clock().UTC()
	}
	channel := map[string]any{"state": "unavailable"}
	if schedule, err := s.DB.Schedule(r.Context()); err == nil {
		within, scopeErr := s.DB.ScheduleWithinScope(r.Context(), schedule, domain.CollectionScope{id})
		entries, err := s.DB.CurrentEntries(r.Context(), schedule, now)
		if err == nil && scopeErr == nil && within && len(schedule.Programs) > 0 {
			state := schedule.At(now, entries)
			gaps := 0
			for i := 1; i < len(schedule.Programs); i++ {
				prior := schedule.Programs[i-1]
				if schedule.Programs[i].Start.After(prior.Start.Add(time.Duration(prior.Duration * float64(time.Second)))) {
					gaps++
				}
			}
			last := schedule.Programs[len(schedule.Programs)-1]
			channel = map[string]any{"state": state.State, "reason": state.Reason, "revision": state.Revision, "horizon": last.Start.Add(time.Duration(last.Duration * float64(time.Second))), "gaps": gaps}
		}
	}
	write(w, 200, map[string]any{"worker": state, "items": counts, "inventory": inventory, "collection": collection, "channel": channel, "metadata_retention": "all snapshots, observations, decisions, and schedules retained; no automatic pruning", "metadata_growth_alert": inventory.DatabaseBytes > 1<<30, "client_playback_status": "unverified by server; consult browser qualification", "last_server_resolution_failure": s.failureStatus(), "video_storage": collection.Policy})
}

func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	e, err := s.DB.Entry(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	if err != nil || e.Policy != domain.Local || !e.Watchable() || s.Media == "" || !s.allowsRequestCollection(r, e.Episode.CollectionID) {
		write(w, 404, map[string]string{"error": "episode unavailable"})
		return
	}
	selected, _ := e.Selected()
	hash := e.LocalSHA256
	var f *os.File
	if e.Decision.ProcessedVideo != nil {
		f, err = video.Verify(s.Media, s.VideoDerivatives, s.VideoReceipts, e)
		hash = e.Decision.ProcessedVideo.SHA256
	} else {
		f, err = preservation.Verify(s.Media, e.LocalPath, e.LocalSHA256, selected.Bytes)
	}
	if err != nil {
		s.failure(r, "local video or receipt failed integrity check")
		write(w, 503, map[string]string{"error": "local video or receipt failed integrity check"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		write(w, 503, map[string]string{"error": "local original unavailable"})
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"sha256-`+hash+`"`)
	http.ServeContent(w, r, selected.Name, info.ModTime(), f)
}

func (s *Server) videoCaption(w http.ResponseWriter, r *http.Request) {
	e, err := s.DB.Entry(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		write(w, 503, map[string]string{"error": "catalog unavailable"})
		return
	}
	index, parseErr := strconv.Atoi(r.PathValue("index"))
	if err != nil || parseErr != nil || !e.Watchable() || e.Decision.ProcessedVideo == nil || index < 0 || index >= len(e.Decision.ProcessedVideo.Captions) || !s.allowsRequestCollection(r, e.Episode.CollectionID) {
		write(w, 404, map[string]string{"error": "caption unavailable"})
		return
	}
	// Check the whole media/receipt binding before honoring a caption cache or
	// Range request. A caption cannot outlive revoked or corrupted source media.
	asset, err := video.Verify(s.Media, s.VideoDerivatives, s.VideoReceipts, e)
	if err != nil {
		write(w, 503, map[string]string{"error": "local video or caption failed integrity check"})
		return
	}
	asset.Close()
	f, err := video.VerifyCaption(s.Media, e, index)
	if err != nil {
		write(w, 503, map[string]string{"error": "local caption failed integrity check"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		write(w, 503, map[string]string{"error": "local caption unavailable"})
		return
	}
	c := e.Decision.ProcessedVideo.Captions[index]
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"sha256-`+c.SHA256+`"`)
	http.ServeContent(w, r, c.File, info.ModTime(), f)
}
