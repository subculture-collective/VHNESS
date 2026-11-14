package audio_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/app"
	audiomodule "git.subcult.tv/subculture-collective/vhness/internal/audio"
	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/music"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Synthetic PCM audio verifies bytes, duration and scheduling, not music fidelity.
func tone(seconds int) []byte {
	count := seconds * 8000
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+count*2))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint32(8000))
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(count*2))
	for i := 0; i < count; i++ {
		binary.Write(&b, binary.LittleEndian, int16(4000*math.Sin(float64(i)*2*math.Pi*440/8000)))
	}
	return b.Bytes()
}
func TestContinuousAudioRightsAndStationIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("disposable database absent; audio integration unverified")
	}
	ctx := context.Background()
	admin, err := catalog.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Close()
	schema := fmt.Sprintf("audio_test_%x", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db := &catalog.DB{Pool: pool}
	if err = db.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatal(err)
	}
	c := domain.Collection{ID: "audio-fixture", Policy: domain.Local, Budget: 1 << 20, Reserve: 1}
	if err = db.Collection(ctx, c); err != nil {
		t.Fatal(err)
	}
	media, derived := t.TempDir(), t.TempDir()
	one, continuous := tone(1), tone(2)
	for _, name := range []string{"one.wav", "two.wav"} {
		if err = os.WriteFile(filepath.Join(media, name), one, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(derived, "continuous.wav"), continuous, 0600); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"metadata":{"identifier":"audio-source"},"files":[{"name":"one.wav"},{"name":"two.wav"}],"retained_unknown":"preserved"}`)
	snapshot := domain.Digest(raw)
	if err = db.Snapshot(ctx, "audio-source", raw, 200, "", "", nil); err != nil {
		t.Fatal(err)
	}
	r := music.Recording{Date: "1977-05-08", ID: "recording", ConcertID: "concert", Title: "Synthetic continuous recording", Source: "Generated tone", Taper: "Fixture generator", Duration: 2, Synthetic: true, Warnings: []string{"Unreviewed set boundary retained"}, Tracks: []music.Track{{Title: "First", Position: 0, Start: 0, Duration: 1, File: music.File{Name: "one.wav", Bytes: int64(len(one))}}, {Title: "Second", Position: 1, Start: 1, Duration: 1, File: music.File{Name: "two.wav", Bytes: int64(len(one))}}}, Sets: []music.Set{{Title: "Reviewed second track", First: 1, Last: 1, Reviewed: true}}}
	rep := domain.Representation{ID: "recording", Media: domain.AudioMedia, SourceItemID: "audio-source", Snapshot: snapshot, File: "one.wav", Collections: []string{c.ID}, Extension: domain.MediaExtension{Audio: &domain.AudioExtension{RecordingID: r.ID, ConcertID: r.ConcertID, Recording: &r, Tracks: []domain.AudioTrack{{ID: "one", File: "one.wav"}, {ID: "two", File: "two.wav"}}, Credits: map[string]string{"taper": r.Taper}}}}
	approval := domain.PreservationApproval{Snapshot: snapshot, IndependentRecoverySHA256: domain.Digest([]byte("synthetic original recovery")), Manifest: []domain.LocalOriginal{{File: "one.wav", Bytes: int64(len(one)), SHA256: domain.Digest(one), Path: "one.wav"}, {File: "two.wav", Bytes: int64(len(one)), SHA256: domain.Digest(one), Path: "two.wav"}}}
	review := domain.TransportReview{Snapshot: snapshot, Files: []string{"one.wav", "two.wav"}, Playback: true, Schedule: true, Notes: "Synthetic private audio review"}
	asset := domain.AudioAsset{Snapshot: snapshot, ManifestSHA256: domain.ManifestDigest(approval), Path: "continuous.wav", SHA256: domain.Digest(continuous), Bytes: int64(len(continuous)), Duration: 2, RecoverySHA256: domain.Digest([]byte("synthetic derived recovery")), Tool: "Synthetic PCM fixture"}
	for _, id := range []string{"first", "future"} {
		r.ID = id
		rep.Extension.Audio.RecordingID = id
		item := domain.CatalogItem{ID: id, Title: r.Title, Collections: []string{c.ID}, Representations: []domain.Representation{rep}}
		if err = db.InsertCatalog(ctx, item, "Synthetic recording mapping"); err != nil {
			t.Fatal(err)
		}
		if err = db.ReviewTransport(ctx, id, rep.ID, c.ID, review, approval); err != nil {
			t.Fatal(err)
		}
		bad := asset
		bad.ManifestSHA256 = domain.Digest([]byte("foreign input inventory"))
		if err = audiomodule.Register(ctx, db, media, derived, id, rep.ID, c.ID, bad, "Reject stale input receipt"); err == nil {
			t.Fatal("foreign input receipt accepted")
		}
		if err = audiomodule.Register(ctx, db, media, derived, id, rep.ID, c.ID, asset, "Verify synthetic continuous asset"); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	schedule := domain.AudioSchedule{Collection: c.ID, Revision: "fixture-station", Programs: []domain.AudioProgram{{Item: "first", Representation: rep.ID, Start: now, Duration: 2}, {Item: "future", Representation: rep.ID, Start: now.Add(2 * time.Second), Duration: 2}}}
	if err = db.StoreAudioSchedule(ctx, schedule, "Synthetic station"); err != nil {
		t.Fatal(err)
	}
	server := &app.Server{Compatibility: "audio", DB: db, Media: media, Derivatives: derived, Collection: c.ID, Clock: func() time.Time { return now.Add(1500 * time.Millisecond) }}
	handler := server.Handler()
	get := func(path string, headers map[string]string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		handler.ServeHTTP(w, r)
		return w
	}

	// Dedhed's frontend shows these bodies to listeners, so they keep the
	// legacy backend's wording.
	offAir := "The station is off the air right now. Try again, or play a set from the library."
	legacyError := func(w *httptest.ResponseRecorder) string {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return body.Error
	}
	for path, want := range map[string]string{
		"/api/recordings?year=77":                                 "Use a four-digit year and at most 200 search characters.",
		"/api/recordings?date_from=1977-02-30":                    "Use complete YYYY-MM-DD date bounds.",
		"/api/recordings?date_from=1977-05-09&date_to=1977-05-08": "The original-date range is reversed.",
		"/api/recordings?page=10001":                              "That page is past the end of the shelf.",
		"/api/recordings/absent":                                  "That tape isn't in the library.",
		"/api/dates/1977-5-8":                                     "That isn't a date the calendar can read.",
		"/api/calendar?year=next":                                 "That year isn't on the calendar.",
	} {
		status := 400
		if strings.HasPrefix(path, "/api/recordings/") {
			status = 404
		}
		if w := get(path, nil); w.Code != status || legacyError(w) != want {
			t.Fatal("legacy error wording", path, w.Code, w.Body.String())
		}
	}
	// A request whose database reads fail reports Dedhed's unavailable wording.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for path, want := range map[string]string{
		"/api/recordings":       "The catalog isn't answering. Try again in a moment.",
		"/api/recordings/first": "The catalog isn't answering. Try again in a moment.",
		"/api/station":          "The station schedule isn't answering. Try again, or play a set from the library.",
		"/api/dates/1977-05-08": "The show index isn't answering. Try again in a moment.",
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil).WithContext(canceled))
		if w.Code != 503 || legacyError(w) != want {
			t.Fatal("legacy unavailable wording", path, w.Code, w.Body.String())
		}
	}

	inventoryRows := []json.RawMessage{}
	for _, id := range []string{"first", "future"} {
		row := map[string]any{"id": id, "show_date": "1977-05-08", "date_text": "1977-05-08", "artist": "Fixture artist", "venue": "Fixture venue", "location": "Fixture location", "title": "Synthetic continuous recording", "kind": "synthetic", "source": "Generated tone", "lineage": "Retained fixture lineage", "taper": "Fixture generator", "transferer": "Fixture transfer", "downloads": 1, "avg_rating": 5, "num_reviews": 1, "item_size": len(continuous), "added_at": now, "tracks": r.Tracks, "songs": "First Second", "duration": r.Duration, "warnings": []string{}, "summary": map[string]any{}, "snapshot_sha256": snapshot, "observed_at": now, "updated_at": now}
		encoded, _ := json.Marshal(row)
		inventoryRows = append(inventoryRows, encoded)
	}
	retained := catalog.RetainedExport{Collection: c.ID, Module: "audio", Recovery: domain.Digest([]byte("independent fixture recovery")), Tables: map[string][]json.RawMessage{"source_items": inventoryRows}, Counts: map[string]int{"source_items": 2}}
	retainedRaw, _ := json.Marshal(retained)
	if err = db.RetainLegacy(ctx, retainedRaw, c.ID, "Retain complete fixture inventory"); err != nil {
		t.Fatal("retained inventory import", err)
	}
	if err = db.RetainLegacy(ctx, retainedRaw, c.ID, "Idempotent recovery import"); err != nil {
		t.Fatal("repeat retained inventory", err)
	}
	if w := get("/api/recordings", nil); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"total":2`)) || !bytes.Contains(w.Body.Bytes(), []byte("Fixture generator")) {
		t.Fatal("legacy audio listing and credits", w.Code, w.Body.String())
	}
	if w := get("/api/recordings/first", nil); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("Reviewed second track")) {
		t.Fatal("retained reviewed set", w.Code, w.Body.String())
	}
	if w := get("/api/audio/first", map[string]string{"Range": "bytes=0-43"}); w.Code != 206 || w.Header().Get("ETag") != `"`+asset.SHA256+`"` || !bytes.Equal(w.Body.Bytes(), continuous[:44]) {
		t.Fatal("legacy audio ranges and validator", w.Code, w.Header())
	}
	var legacyState music.Now
	if w := get("/api/station", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &legacyState) != nil || legacyState.Program.RecordingID != "first" || legacyState.Revision != schedule.Revision || legacyState.Offset != 1.5 || legacyState.Track != 1 || legacyState.Next == nil || legacyState.Next.RecordingID != "future" {
		t.Fatal("legacy station timeline", w.Code, w.Body.String())
	}
	// Unchanged inputs and the derivative are hashed once, not on every range
	// read or station poll. Files written within the last second are always
	// rehashed, so let the fixtures age first.
	time.Sleep(1100 * time.Millisecond)
	if w := get("/api/audio/first", nil); w.Code != 200 {
		t.Fatal("warm verification", w.Code)
	}
	warm := server.AudioFiles.Hashed()
	if warm == 0 {
		t.Fatal("audio routes bypassed the verified-file state")
	}
	for i := 0; i < 3; i++ {
		if w := get("/api/audio/first", map[string]string{"Range": "bytes=44-"}); w.Code != 206 || w.Header().Get("ETag") != `"`+asset.SHA256+`"` || !bytes.Equal(w.Body.Bytes(), continuous[44:]) {
			t.Fatal("verified range read", w.Code, w.Header())
		}
		if w := get("/api/station", nil); w.Code != 200 {
			t.Fatal("verified station poll", w.Code, w.Body.String())
		}
		if w := get("/api/audio/channel", nil); !bytes.Contains(w.Body.Bytes(), []byte(`"state":"playing"`)) {
			t.Fatal("verified native station poll", w.Body.String())
		}
	}
	if hashed := server.AudioFiles.Hashed(); hashed != warm {
		t.Fatal("unchanged audio files reread per request", warm, hashed)
	}
	sameSize := bytes.Clone(continuous)
	sameSize[len(sameSize)-1] ^= 0xff
	if err = os.WriteFile(filepath.Join(derived, "continuous.wav"), sameSize, 0600); err != nil {
		t.Fatal(err)
	}
	if w := get("/api/audio/first", map[string]string{"Range": "bytes=0-43"}); w.Code != 503 {
		t.Fatal("changed derivative served from earlier verification", w.Code)
	}
	if w := get("/api/station", nil); w.Code != 503 || legacyError(w) != offAir {
		t.Fatal("station accepted changed derivative", w.Code, w.Body.String())
	}
	if err = os.WriteFile(filepath.Join(derived, "continuous.wav"), continuous, 0600); err != nil {
		t.Fatal(err)
	}
	if w := get("/api/audio/first", map[string]string{"If-None-Match": `"` + asset.SHA256 + `"`}); w.Code != 304 {
		t.Fatal("restored derivative conditional read", w.Code)
	}
	for _, path := range []string{"/api/calendar?year=1977", "/api/on-this-day?month=5&day=8", "/api/shows?q=Fixture&year=1977", "/api/dates/1977-05-08"} {
		if w := get(path, nil); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("1977")) {
			t.Fatal("retained inventory contract", path, w.Code, w.Body.String())
		}
	}
	base := "/api/catalog/v1/items/first/representations/recording/audio"
	w := get(base+"/resolve", nil)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("Fixture generator")) || bytes.Contains(w.Body.Bytes(), []byte("continuous.wav")) {
		t.Fatal("audio contract or private path exposure", w.Code, w.Body.String())
	}
	w = get(base+"/media", map[string]string{"Range": "bytes=0-43"})
	if w.Code != 206 || !bytes.HasPrefix(w.Body.Bytes(), []byte("RIFF")) {
		t.Fatal("continuous range playback", w.Code)
	}
	w = get("/api/audio/channel", nil)
	var station struct {
		State    string  `json:"state"`
		Offset   float64 `json:"offset"`
		Revision string  `json:"revision"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &station); err != nil || station.State != "playing" || station.Offset != 1.5 {
		t.Fatal("server station clock", err, w.Code, w.Body.String())
	}
	if w = get(base+"/resolve?collection=foreign", nil); w.Code != 404 {
		t.Fatal("foreign audio exposed", w.Code)
	}
	server.Collection = ""
	server.Collections = domain.CollectionScope{c.ID, "another-allowed"}
	if w = get(base+"/resolve?collection="+c.ID, nil); w.Code != 200 {
		t.Fatal("allowed multi scope audio unavailable", w.Code)
	}
	if w = get(base+"/resolve", nil); w.Code != 404 {
		t.Fatal("ambiguous audio collection accepted", w.Code)
	}
	if w = get(base+"/media?collection=foreign", nil); w.Code != 404 {
		t.Fatal("audio bytes crossed multi allowlist", w.Code)
	}
	server.Collection = c.ID
	server.Collections = nil
	review.Schedule = false
	review.Notes = "Withdraw future station eligibility"
	if err = db.ReviewTransport(ctx, "future", rep.ID, c.ID, review, approval); err != nil {
		t.Fatal(err)
	}
	if w := get("/api/station", nil); w.Code != 503 || legacyError(w) != offAir {
		t.Fatal("legacy station ignored future withdrawal", w.Code, w.Body.String())
	}
	w = get("/api/audio/channel", nil)
	if !bytes.Contains(w.Body.Bytes(), []byte(`"state":"unavailable"`)) {
		t.Fatal("withdrawn future program exposed station", w.Body.String())
	}
	if w = get(base+"/resolve", nil); w.Code != 200 {
		t.Fatal("station withdrawal removed independent on-demand playback", w.Code)
	}
	review.Playback = false
	review.Notes = "Withdraw first recording playback"
	if err = db.ReviewTransport(ctx, "first", rep.ID, c.ID, review, approval); err != nil {
		t.Fatal(err)
	}
	if w := get("/api/audio/first", map[string]string{"If-None-Match": `"` + asset.SHA256 + `"`}); w.Code != 404 {
		t.Fatal("legacy conditional media ignored withdrawal", w.Code)
	}
	if w := get("/api/recordings/first", nil); w.Code != 404 || legacyError(w) != "That tape isn't in the library." {
		t.Fatal("legacy detail ignored withdrawal", w.Code, w.Body.String())
	}
	if w = get(base+"/media", map[string]string{"If-None-Match": `"sha256-` + asset.SHA256 + `"`}); w.Code != 404 {
		t.Fatal("revoked conditional audio exposed", w.Code)
	}
	review.Playback = true
	review.Notes = "Restore synthetic playback"
	if err = db.ReviewTransport(ctx, "first", rep.ID, c.ID, review, approval); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(media, "two.wav"), []byte("corrupt input"), 0600); err != nil {
		t.Fatal(err)
	}
	if w = get(base+"/media", nil); w.Code != 404 {
		t.Fatal("continuous derivative survived corrupt source input", w.Code)
	}
	if err = os.WriteFile(filepath.Join(media, "two.wav"), one, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(derived, "continuous.wav"), []byte("corrupt derivative"), 0600); err != nil {
		t.Fatal(err)
	}
	if w = get(base+"/media", nil); w.Code != 404 {
		t.Fatal("corrupt continuous derivative exposed", w.Code)
	}

	// A longer synthetic recording exercises bounded public renewal and repeat
	// delivery without publishing any real material or relying on wall time.
	long := tone(10)
	if err = os.WriteFile(filepath.Join(media, "one.wav"), long, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(derived, "renewal.wav"), long, 0600); err != nil {
		t.Fatal(err)
	}
	r.ID = "renewal"
	r.Duration = 10
	r.Tracks = []music.Track{{Title: "Synthetic renewal", Position: 0, Duration: 10, File: music.File{Name: "one.wav", Bytes: int64(len(long))}}}
	r.Sets = nil
	rep.Extension.Audio.Recording = &r
	rep.Extension.Audio.RecordingID = r.ID
	rep.Extension.Audio.Tracks = []domain.AudioTrack{{ID: "one", File: "one.wav"}}
	approval.Manifest = []domain.LocalOriginal{{File: "one.wav", Bytes: int64(len(long)), SHA256: domain.Digest(long), Path: "one.wav"}}
	asset.Path = "renewal.wav"
	asset.Duration = 10
	asset.Bytes = int64(len(long))
	asset.SHA256 = domain.Digest(long)
	asset.ManifestSHA256 = domain.ManifestDigest(approval)
	review.Files = []string{"one.wav"}
	review.Playback = true
	review.Schedule = true
	review.Public = true
	review.Notes = "Explicit synthetic public renewal fixture"
	item := domain.CatalogItem{ID: r.ID, Title: r.Title, Collections: []string{c.ID}, Representations: []domain.Representation{rep}}
	if err = db.InsertCatalog(ctx, item, "Synthetic renewal mapping"); err != nil {
		t.Fatal(err)
	}
	if err = db.ReviewPublicTransport(ctx, item.ID, rep.ID, c.ID, review, approval); err != nil {
		t.Fatal(err)
	}
	if err = audiomodule.Register(ctx, db, media, derived, item.ID, rep.ID, c.ID, asset, "Synthetic renewal asset"); err != nil {
		t.Fatal(err)
	}
	selected := []audiomodule.ScheduleSelection{{Item: item.ID, Representation: rep.ID}}
	renewed, err := audiomodule.RenewPublicSchedule(ctx, db, media, derived, c.ID, selected, 1, now.Add(10*time.Minute), "Retain exact synthetic selection")
	if err != nil || len(renewed.Programs) != 360 || !renewed.Programs[0].Start.Equal(now) {
		t.Fatal("bounded public renewal", err, len(renewed.Programs))
	}
	repeated, err := audiomodule.RenewPublicSchedule(ctx, db, media, derived, c.ID, selected, 1, now.Add(20*time.Minute), "Repeat exact synthetic selection")
	if err != nil || repeated.Revision != renewed.Revision {
		t.Fatal("repeat renewal changed timeline", err)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM audio_schedules WHERE collection_id=$1 AND revision=$2`, c.ID, renewed.Revision).Scan(&count); err != nil || count != 1 {
		t.Fatal("repeat renewal duplicated history", err, count)
	}
	if _, err = audiomodule.RenewPublicSchedule(ctx, db, media, derived, c.ID, append(selected, selected...), 1, now, "Reject duplicate selection"); err == nil {
		t.Fatal("duplicate renewal accepted")
	}
	if err = os.WriteFile(filepath.Join(derived, "renewal.wav"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = audiomodule.RenewPublicSchedule(ctx, db, media, derived, c.ID, selected, 1, now, "Reject changed media"); err == nil {
		t.Fatal("corrupt renewal media accepted")
	}
	if err = os.WriteFile(filepath.Join(derived, "renewal.wav"), long, 0600); err != nil {
		t.Fatal(err)
	}
	review.Public = false
	review.Notes = "Withdraw synthetic public eligibility"
	if err = db.ReviewTransport(ctx, item.ID, rep.ID, c.ID, review, approval); err != nil {
		t.Fatal(err)
	}
	if _, err = audiomodule.RenewPublicSchedule(ctx, db, media, derived, c.ID, selected, 1, now, "Reject private selection"); err == nil {
		t.Fatal("private renewal recording accepted")
	}
}
