package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPublicPublicationIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL absent; public integration unverified")
	}
	ctx := context.Background()
	admin, err := catalog.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Close()
	schema := "public_http_" + time.Now().UTC().Format("20060102150405")
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Exec(ctx, "DROP SCHEMA "+name+" CASCADE")
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	operator := &catalog.DB{Pool: pool}
	if err = operator.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatal(err)
	}
	c := domain.Collection{ID: "public-fixture", Policy: domain.Local, Budget: 1 << 20, Reserve: 1}
	if err = operator.Collection(ctx, c); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	content := []byte("synthetic public-boundary fixture bytes")
	hash := domain.Digest(content)
	if err = os.WriteFile(filepath.Join(root, "original.pdf"), content, 0600); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"files":[{"name":"original.pdf"}]}`)
	if err = operator.Snapshot(ctx, "print-source", raw, 200, "", "", nil); err != nil {
		t.Fatal(err)
	}
	rep := domain.Representation{ID: "pdf", Media: domain.PrintMedia, SourceItemID: "print-source", Snapshot: domain.Digest(raw), File: "original.pdf", Collections: []string{c.ID}, Extension: domain.MediaExtension{Print: &domain.PrintExtension{DocumentID: "print", Variant: "original", Pages: 1}}}
	legacyEditorial := json.RawMessage(`{"legacy_document":{"published":true,"source_item":"print-source","source_key":"original","rights":"unknown-rights","review_note":"Private operator note","metadata":{"title":"Reviewed fixture print","creator":"Fixture author","language":"en","subjects":["print"],"private":"operator-only"},"overrides":{}}}`)
	item := domain.CatalogItem{Editorial: legacyEditorial, ID: "print", Title: "Reviewed fixture print", Creator: "Fixture author", Date: "2026-10-03", Collections: []string{c.ID}, Representations: []domain.Representation{rep}}
	if err = operator.InsertCatalog(ctx, item, "Private mapping"); err != nil {
		t.Fatal(err)
	}
	review := domain.TransportReview{Snapshot: rep.Snapshot, Files: []string{rep.File}, Read: true, Download: true, Notes: "Private original review"}
	approval := domain.PreservationApproval{Snapshot: rep.Snapshot, IndependentRecoverySHA256: domain.Digest([]byte("fixture restore")), Manifest: []domain.LocalOriginal{{File: rep.File, Bytes: int64(len(content)), SHA256: hash, Path: "original.pdf"}}}
	if err = operator.ReviewTransport(ctx, item.ID, rep.ID, c.ID, review, approval); err != nil {
		t.Fatal(err)
	}
	videoCollection := domain.Collection{ID: "video-fixture", Policy: domain.Stream}
	if err = operator.Collection(ctx, videoCollection); err != nil {
		t.Fatal(err)
	}
	video := domain.Episode{ID: "video", CollectionID: videoCollection.ID, Title: "Reviewed fixture video", Files: []domain.File{{Name: "video.mp4", Bytes: 100, Duration: 10}, {Name: "other.mp4", Bytes: 100, Duration: 10}}}
	videoRaw := []byte(`{"files":[{"name":"video.mp4"},{"name":"other.mp4"}]}`)
	if err = operator.Snapshot(ctx, video.ID, videoRaw, 200, "", "", &video); err != nil {
		t.Fatal(err)
	}
	decision := domain.Decision{Published: true, Playback: true, Channel: true, File: "video.mp4", Notes: "Private playback"}
	if err = operator.Decide(ctx, video.ID, decision); err != nil {
		t.Fatal(err)
	}
	public := &catalog.DB{Pool: pool, PublicOnly: true}
	server := &Server{DB: public, Media: root, Collections: domain.CollectionScope{c.ID, videoCollection.ID}}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	count := func(want int) {
		t.Helper()
		w := get("/api/catalog/v1/items")
		var p catalog.CatalogPage
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 || p.Total != want {
			t.Fatalf("public catalog: %d %s", w.Code, w.Body.String())
		}
	}
	count(0)
	creators := func(want string) {
		t.Helper()
		if w := get("/api/catalog/v1/creators"); w.Code != 200 || w.Body.String() != want+"\n" {
			t.Fatalf("public creators: %d %s", w.Code, w.Body.String())
		}
	}
	creators("[]")
	printPath := "/api/catalog/v1/items/print/representations/pdf"
	for _, path := range []string{"/api/episodes/video", "/api/playback/video", "/api/media/video", printPath + "/resolve?collection=" + c.ID, printPath + "/media?collection=" + c.ID, printPath + "/print?collection=" + c.ID, printPath + "/cover?collection=" + c.ID, printPath + "/search?q=fixture&collection=" + c.ID, "/api/status?collection=" + c.ID} {
		if w := get(path); w.Code != 404 {
			t.Fatalf("private path exposed: %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := get("/api/catalog/v1/collections"); strings.Contains(w.Body.String(), c.ID) || strings.Contains(w.Body.String(), videoCollection.ID) {
		t.Fatal("private collections exposed", w.Body.String())
	}
	review.Public = true
	if operator.ReviewTransport(ctx, item.ID, rep.ID, c.ID, review, approval) == nil {
		t.Fatal("private review path silently approved publication")
	}
	if err = operator.ReviewPublicTransport(ctx, item.ID, rep.ID, c.ID, review, approval); err != nil {
		t.Fatal(err)
	}
	count(1)
	creators(`[{"name":"Fixture author","items":1}]`)
	if w := get("/api/catalog/v1/creators?collection=elsewhere"); w.Code != 404 {
		t.Fatal("creators outside the collection scope", w.Code)
	}
	if !server.communityAvailable(ctx, c.ID, "catalog:"+item.ID) || server.communityAvailable(ctx, videoCollection.ID, "catalog:"+item.ID) {
		t.Fatal("catalog favorite bypassed collection/public availability")
	}
	for _, path := range []string{"/api/catalog/v1/items?date_from=2026-02-30", "/api/catalog/v1/items?date_from=2026-10-03&date_to=2026-01-01"} {
		if w := get(path); w.Code != 400 {
			t.Fatal("invalid mixed date accepted", w.Code)
		}
	}
	if w := get(printPath + "/resolve?collection=" + c.ID); w.Code != 200 {
		t.Fatal("approved print resolution", w.Code, w.Body.String())
	}
	if w := get(printPath + "/media?collection=" + c.ID); w.Code != 200 || string(content) != w.Body.String() {
		t.Fatal("approved original bytes", w.Code, w.Body.String())
	}

	index := domain.PrintIndex{Snapshot: rep.Snapshot, OriginalSHA256: hash, Pages: 1, TextStatus: "page-text", Tool: "Synthetic retained fixture"}
	if err = operator.StorePrintIndex(ctx, item.ID, rep.ID, rep.File, index, map[int]string{1: "A retained OCR passage about solidarity."}); err != nil {
		t.Fatal("retained print index", err)
	}
	retained := catalog.RetainedExport{Collection: c.ID, Module: "print", Recovery: domain.Digest([]byte("independent fixture restore")), Tables: map[string][]json.RawMessage{"collections": {json.RawMessage(`{"id":"public-fixture","config":{"name":"Fixture print","introduction":"Retained introduction","accent":"red"}}`)}}, Counts: map[string]int{"collections": 1}}
	retainedRaw, _ := json.Marshal(retained)
	if err = operator.RetainLegacy(ctx, retainedRaw, c.ID, "Retain original print configuration"); err != nil {
		t.Fatal("retain print export", err)
	}
	if _, _, err = public.LegacyPrintDocuments(ctx, c.ID, "", "", "", "", 1, 0); err != nil {
		t.Fatal("compatibility print SQL", err)
	}
	server.Compatibility = "print"
	server.Collections = domain.CollectionScope{c.ID}
	if w := get("/api/collections"); w.Code != 200 || !strings.Contains(w.Body.String(), "Retained introduction") {
		t.Fatal("compatibility collections", w.Code, w.Body.String())
	}
	if w := get("/api/documents?q=solidarity"); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) || strings.Contains(w.Body.String(), "operator-only") || strings.Contains(w.Body.String(), "Private operator note") {
		t.Fatal("retained metadata/OCR search", w.Code, w.Body.String())
	}
	if w := get("/api/facets"); w.Code != 200 || w.Body.String() != "{\"creators\":[{\"name\":\"Fixture author\",\"items\":1}],\"series\":[],\"subjects\":[\"print\"]}\n" {
		t.Fatal("compatibility facets", w.Code, w.Body.String())
	}
	if w := get("/api/documents/print"); w.Code != 200 || !strings.Contains(w.Body.String(), "unknown-rights") || !strings.Contains(w.Body.String(), `"pages":1`) {
		t.Fatal("retained print detail", w.Code, w.Body.String())
	}
	if w := get("/api/assets/pdf?download=1"); w.Code != 200 || w.Header().Get("ETag") != `"`+hash+`"` || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || w.Body.String() != string(content) {
		t.Fatal("legacy original/download bytes", w.Code, w.Header(), w.Body.String())
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/assets/pdf", nil)
	request.Header.Set("Range", "bytes=0-3")
	server.Handler().ServeHTTP(w, request)
	if w.Code != 206 || w.Body.String() != string(content[:4]) {
		t.Fatal("legacy PDF byte range", w.Code, w.Body.String())
	}
	if w := get("/api/assets/pdf/search?q=solidarity"); w.Code != 200 || !strings.Contains(w.Body.String(), `"variant":"pdf"`) || !strings.Contains(w.Body.String(), `"page":1`) {
		t.Fatal("legacy OCR search shape", w.Code, w.Body.String())
	}
	if w := get("/api/documents?collection=" + videoCollection.ID); w.Code != 404 {
		t.Fatal("compatibility scope bypass", w.Code)
	}
	server.Compatibility = ""
	server.Collections = domain.CollectionScope{c.ID, videoCollection.ID}
	if err = operator.ReviewPublicVideo(ctx, video.ID, true, "Owner-approved fixture publication"); err != nil {
		t.Fatal(err)
	}
	count(2)
	if w := get("/api/playback/video"); w.Code != 200 {
		t.Fatal("approved source video", w.Code, w.Body.String())
	}
	privateVideo := video
	privateVideo.ID = "private-future-video"
	if err = operator.Snapshot(ctx, privateVideo.ID, []byte(`{"private_fixture":true,"files":[{"name":"video.mp4"}]}`), 200, "", "", &privateVideo); err != nil {
		t.Fatal(err)
	}
	if err = operator.Decide(ctx, privateVideo.ID, decision); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	schedule := domain.Schedule{Revision: "public-boundary", Programs: []domain.Program{
		{EpisodeID: video.ID, File: decision.File, Policy: domain.Stream, Start: start, Duration: 10},
		{EpisodeID: privateVideo.ID, File: decision.File, Policy: domain.Stream, Start: start.Add(10 * time.Second), Duration: 10},
	}}
	if err = operator.SaveSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	server.Clock = func() time.Time { return start.Add(time.Second) }
	if w := get("/api/channel?collection=" + videoCollection.ID); w.Code != 200 || strings.Contains(w.Body.String(), privateVideo.ID) || strings.Contains(w.Body.String(), `"playing"`) {
		t.Fatal("private future program exposed", w.Code, w.Body.String())
	}
	if w := get("/api/playback/" + privateVideo.ID); w.Code != 404 {
		t.Fatal("private playback exposed", w.Code)
	}
	if err = operator.ReviewPublicVideo(ctx, video.ID, false, "Withdraw fixture publication"); err != nil {
		t.Fatal(err)
	}
	count(1)
	if w := get("/api/playback/video"); w.Code != 404 {
		t.Fatal("revocation ignored", w.Code)
	}
	if err = operator.ReviewPublicVideo(ctx, video.ID, true, "Reapprove exact current source"); err != nil {
		t.Fatal(err)
	}
	decision.File = "other.mp4"
	if err = operator.Decide(ctx, video.ID, decision); err != nil {
		t.Fatal(err)
	}
	count(1)
	if w := get("/api/playback/video"); w.Code != 404 {
		t.Fatal("selected file change retained publication", w.Code)
	}
	decision.File = "video.mp4"
	if err = operator.Decide(ctx, video.ID, decision); err != nil {
		t.Fatal(err)
	}
	if err = operator.Snapshot(ctx, video.ID, append(videoRaw, ' '), 200, "", "", &video); err != nil {
		t.Fatal(err)
	}
	count(1)
	if w := get("/api/episodes/video"); w.Code != 404 {
		t.Fatal("source refresh retained old publication", w.Code)
	}
	newRaw := append(raw, ' ')
	if err = operator.Snapshot(ctx, "print-source", newRaw, 200, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE catalog_representations SET source_hash=$1 WHERE item_id=$2", domain.Digest(newRaw), item.ID); err != nil {
		t.Fatal(err)
	}
	count(0)
	if w := get(printPath + "/media?collection=" + c.ID); w.Code != 404 {
		t.Fatal("stale print approval served bytes", w.Code)
	}
	if p, err := operator.Catalog(ctx, catalog.CatalogQuery{Page: 1}); err != nil || p.Total != 3 {
		t.Fatal("public filtering changed private catalog", err, p.Total)
	}
	server.Collections = nil
	if w := get("/api/playback/video?collection=" + videoCollection.ID); w.Code != 404 {
		t.Fatal("public server accepted unrestricted scope", w.Code)
	}
	// Public filters must apply before pagination, without exposing partial dates or private rows.
	server.Collections = domain.CollectionScope{c.ID}
	for i := 0; i < 30; i++ {
		next := domain.CatalogItem{ID: fmt.Sprintf("filter-%02d", i), Title: "Mixed needle", Creator: "Filter author", Date: "2026-10-03", Collections: []string{c.ID}}
		if i == 27 {
			next.Date = "2026"
		}
		if i == 28 {
			next.Date = ""
		}
		if i == 29 {
			next.Creator = "Different creator"
		}
		next.Editorial, _ = json.Marshal(map[string]any{"legacy_document": map[string]any{"published": true, "metadata": map[string]any{"title": next.Title, "creator": next.Creator, "date": next.Date}, "overrides": map[string]any{"date": "1900-01-01"}}})
		copyRep := rep
		copyRep.Snapshot = domain.Digest(newRaw)
		next.Representations = []domain.Representation{copyRep}
		if err := operator.InsertCatalog(ctx, next, "Disposable filter fixture"); err != nil {
			t.Fatal(err)
		}
		copyReview := review
		copyReview.Snapshot = copyRep.Snapshot
		copyApproval := approval
		copyApproval.Snapshot = copyRep.Snapshot
		if err := operator.ReviewPublicTransport(ctx, next.ID, copyRep.ID, c.ID, copyReview, copyApproval); err != nil {
			t.Fatal(err)
		}
	}
	w = get("/api/catalog/v1/items?collection=" + c.ID + "&media=print&q=needle&creator=filter&date_from=2026-10-03&date_to=2026-10-03&page=2")
	var filtered catalog.CatalogPage
	if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil || w.Code != 200 || filtered.Total != 27 || len(filtered.Items) != 3 {
		t.Fatal("combined mixed filters after pagination", w.Code, w.Body.String())
	}
	server.Compatibility = "print"
	for _, path := range []string{"/api/documents?date_from=2026-02-30", "/api/documents?date_from=2026-10-03&date_to=2026-01-01"} {
		if w := get(path); w.Code != 400 {
			t.Fatal("invalid legacy print date accepted", w.Code, w.Body.String())
		}
	}
	w = get("/api/documents?creator=filter&date_from=2026-10-03&date_to=2026-10-03&limit=20&offset=20")
	var documents struct {
		Total     int `json:"total"`
		Documents []struct {
			OriginalDate string `json:"original_date"`
			Metadata     struct {
				Date string `json:"date"`
			} `json:"metadata"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &documents); err != nil || w.Code != 200 || documents.Total != 27 || len(documents.Documents) != 7 {
		t.Fatal("legacy filters after pagination", w.Code, w.Body.String())
	}
	for _, document := range documents.Documents {
		if document.OriginalDate != "2026-10-03" || document.Metadata.Date != "1900-01-01" {
			t.Fatal("source original date conflated with editorial date", document)
		}
	}
	w = get("/api/documents?creator=filter")
	if err := json.Unmarshal(w.Body.Bytes(), &documents); err != nil || documents.Total != 29 {
		t.Fatal("unbounded legacy partial dates excluded", w.Body.String())
	}
	if server.communityAvailable(ctx, c.ID, "catalog:"+item.ID) {
		t.Fatal("stale public item remained available for favorites")
	}
	// The stale item's creator no longer counts on either API.
	publicCreators := `[{"name":"Different creator","items":1},{"name":"Filter author","items":29}]`
	creators(publicCreators)
	if w := get("/api/facets"); w.Code != 200 || !strings.Contains(w.Body.String(), `"creators":`+publicCreators) {
		t.Fatal("compatibility creator facets", w.Code, w.Body.String())
	}
	// An accepted series lists its public issues in issue order on both APIs.
	if err = operator.ImportLabels(ctx, []domain.Label{{ID: "riot", Kind: domain.SeriesKind, Label: "Riot Grrrl"}}); err != nil {
		t.Fatal(err)
	}
	issues := []string{"filter-05", "filter-02", "filter-09"}
	var proposals []domain.LabelProposal
	var decisions []domain.LabelDecision
	for i, id := range issues {
		position := i + 1
		proposals = append(proposals, domain.LabelProposal{Item: id, Label: "riot", Method: "editor", IssuePosition: &position, IssueLabel: fmt.Sprintf("Volume %d", position)})
		decisions = append(decisions, domain.LabelDecision{Item: id, Label: "riot", State: "accepted"})
	}
	if _, err = operator.ProposeLabels(ctx, proposals, "Synthetic series"); err != nil {
		t.Fatal(err)
	}
	if _, err = operator.ReviewLabels(ctx, decisions, "Synthetic series review"); err != nil {
		t.Fatal(err)
	}
	server.Compatibility = ""
	w = get("/api/catalog/v1/items?label=riot")
	if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil || w.Code != 200 || filtered.Total != 3 {
		t.Fatal("series catalog filter", w.Code, w.Body.String())
	}
	for i, id := range issues {
		got := filtered.Items[i]
		if got.ID != id || len(got.Labels) != 1 || got.Labels[0].IssueLabel != fmt.Sprintf("Volume %d", i+1) || got.Labels[0].IssuePosition == nil || *got.Labels[0].IssuePosition != i+1 {
			t.Fatal("series catalog issue order", i, got)
		}
	}
	server.Compatibility = "print"
	w = get("/api/documents?series=riot")
	var seriesDocs struct {
		Total     int `json:"total"`
		Documents []struct {
			ID     string `json:"id"`
			Series []struct {
				Slug       string `json:"slug"`
				IssueLabel string `json:"issue_label"`
			} `json:"series"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &seriesDocs); err != nil || w.Code != 200 || seriesDocs.Total != 3 {
		t.Fatal("compatibility series filter", w.Code, w.Body.String())
	}
	for i, id := range issues {
		got := seriesDocs.Documents[i]
		if got.ID != id || len(got.Series) != 1 || got.Series[0].Slug != "riot" || got.Series[0].IssueLabel != fmt.Sprintf("Volume %d", i+1) {
			t.Fatal("compatibility series issue order", i, got)
		}
	}
	if w := get("/api/documents?series=Not%20A%20Slug"); w.Code != 400 {
		t.Fatal("invalid series accepted", w.Code)
	}
	if w := get("/api/facets"); w.Code != 200 || !strings.Contains(w.Body.String(), `"series":[{"slug":"riot","name":"Riot Grrrl"}]`) {
		t.Fatal("compatibility series facets", w.Code, w.Body.String())
	}

}
