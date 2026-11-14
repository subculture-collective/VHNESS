package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMetadataIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; database integration unverified")
	}
	ctx := context.Background()
	d, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Pool.Close()
	schema := "vhness_test_" + time.Now().UTC().Format("20060102150405") + "_" + domain.Digest([]byte(t.Name()))[:8]
	admin := d.Pool
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+name+" CASCADE")
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
	d = &DB{Pool: pool}
	if err = d.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatal("repeat migration", err)
	}
	dir := t.TempDir()
	b, err := os.ReadFile("../../../migrations/0001_metadata.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "0001_metadata.sql"), append(b, []byte("\n-- altered\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if d.Migrate(ctx, dir) == nil {
		t.Fatal("changed migration accepted")
	}
	if err = d.Collection(ctx, domain.Collection{ID: domain.DemocracyNow, Policy: domain.Stream}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Pool.Exec(ctx, `UPDATE collections SET policy='local_preserve',budget=10,reserve=1 WHERE id=$1`, domain.DemocracyNow); err == nil {
		t.Fatal("DB Democracy policy protection missing")
	}
	local := domain.Collection{ID: "budget-proof", Policy: domain.Local, Budget: 512, Reserve: 100}
	if err = d.Collection(ctx, local); err != nil {
		t.Fatal(err)
	}
	if err = d.ReviewBudget(ctx, local.ID, 512, 100, 1024, 100, "Owner approved expansion after capacity measurement"); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []struct {
		id                                string
		prior, reserve, next, nextReserve int64
		notes                             string
	}{
		{local.ID, 512, 100, 2048, 100, "Stale authorization"},
		{local.ID, 1024, 100, 512, 100, "Would decrease original budget"},
		{local.ID, 1024, 100, 2048, 50, "Would reduce reserve"},
		{local.ID, 1024, 100, 2048, 100, ""},
		{domain.DemocracyNow, 0, 0, 2048, 100, "Must never activate local budget"},
	} {
		if d.ReviewBudget(ctx, wrong.id, wrong.prior, wrong.reserve, wrong.next, wrong.nextReserve, wrong.notes) == nil {
			t.Fatal("invalid budget review accepted", wrong)
		}
	}
	var reviewCount int
	if err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM collection_budget_reviews WHERE collection_id=$1 AND old_budget=512 AND new_budget=1024`, local.ID).Scan(&reviewCount); err != nil || reviewCount != 1 {
		t.Fatal("budget history", err, reviewCount)
	}
	raw := []byte("{\n\"metadata\":{\"identifier\":\"item\",\"mediatype\":\"movies\",\"collection\":[\"democracy_now_vid\"],\"title\":\"Title\",\"unknown\":42},\"files\":[{\"name\":\"a.mp4\",\"size\":\"123\",\"length\":\"60\",\"custom\":true}]}\n")
	e, err := source.Normalize("item", domain.DemocracyNow, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Snapshot(ctx, e.ID, raw, 200, "etag", "", &e); err != nil {
		t.Fatal(err)
	}
	decision := domain.Decision{Published: true, Playback: true, Channel: true, File: "a.mp4", Title: "Editorial title", Notes: "Private streaming review"}
	if err = d.Decide(ctx, e.ID, decision); err != nil {
		t.Fatal(err)
	}
	// Listing summaries read only the instance's collections when scoped.
	for scope, want := range map[string]int{"": 1, domain.DemocracyNow: 1, local.ID: 0} {
		var collections []string
		if scope != "" {
			collections = []string{scope}
		}
		summaries, err := d.ScopedSummaries(ctx, collections)
		if err != nil || len(summaries) != want {
			t.Fatal("scoped summaries", scope, len(summaries), err)
		}
	}

	// A reviewed selection becomes a durable draft without acquiring or publishing
	// media, modifying the legacy video decision, or adding target membership.
	page, err := d.Catalog(ctx, CatalogQuery{ID: "legacy-video:item", Page: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("curation source", err, page)
	}
	denied := map[string]bool{"read": false, "playback": false, "download": false, "acquisition": false, "relay": false, "schedule": false, "public": false}
	manifest := CurationManifest{Schema: "arkhiv.curation.v1", Intent: "reviewed-curation", Target: local.ID, Access: denied, Mappings: []CurationMapping{{Item: page.Items[0], Evidence: strings.Repeat("a", 64), Notes: "Reviewed source identity and credits", Access: denied}}}
	draftRaw, _ := json.Marshal(manifest)
	sha, err := d.PrepareCuration(ctx, draftRaw, nil, "Reviewed Kollektor import")
	if err != nil || sha != domain.Digest(draftRaw) {
		t.Fatal("prepare curation", sha, err)
	}
	if _, err = d.PrepareCuration(ctx, draftRaw, nil, "Repeat same draft"); err != nil {
		t.Fatal("idempotent curation", err)
	}
	var drafts, memberships, transports int
	if err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM curation_drafts`).Scan(&drafts); err != nil || drafts != 1 {
		t.Fatal("draft retention", err, drafts)
	}
	_ = d.Pool.QueryRow(ctx, `SELECT count(*) FROM catalog_memberships`).Scan(&memberships)
	_ = d.Pool.QueryRow(ctx, `SELECT count(*) FROM representation_transport_reviews`).Scan(&transports)
	if memberships != 0 || transports != 0 {
		t.Fatal("curation silently granted membership or transport")
	}
	if _, err = d.PrepareCuration(ctx, draftRaw, domain.CollectionScope{domain.DemocracyNow}, "Outside target scope"); err == nil {
		t.Fatal("out-of-scope curation accepted")
	}
	changedCuration := manifest
	changedCuration.Mappings = append([]CurationMapping{}, manifest.Mappings...)
	changedCuration.Mappings[0].Item.Representations = append([]domain.Representation{}, page.Items[0].Representations...)
	changedCuration.Mappings[0].Item.Representations[0].Snapshot = strings.Repeat("b", 64)
	bad, _ := json.Marshal(changedCuration)
	if _, err = d.PrepareCuration(ctx, bad, nil, "Changed source"); err == nil {
		t.Fatal("changed curation source accepted")
	}
	grant := manifest
	grant.Access = map[string]bool{"public": true}
	bad, _ = json.Marshal(grant)
	if _, err = d.PrepareCuration(ctx, bad, nil, "No public authority"); err == nil {
		t.Fatal("public grant in curation accepted")
	}

	var recovered []byte
	if err = d.Pool.QueryRow(ctx, `SELECT raw FROM snapshots WHERE sha256=$1`, domain.Digest(raw)).Scan(&recovered); err != nil || string(recovered) != string(raw) {
		t.Fatal("raw snapshot fidelity", err)
	}
	var changed map[string]any
	if err = json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed["metadata"].(map[string]any)["title"] = "Source changed"
	newRaw, _ := json.Marshal(changed)
	e, err = source.Normalize("item", domain.DemocracyNow, newRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Snapshot(ctx, e.ID, newRaw, 200, "new", "", &e); err != nil {
		t.Fatal(err)
	}
	entries, err := d.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entries["item"].Episode.Title != "Editorial title" || entries["item"].Decision.Notes != decision.Notes {
		t.Fatal("refresh overwrote review")
	}
	var count int
	if err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM snapshots`).Scan(&count); err != nil || count != 2 {
		t.Fatal("snapshot history", err, count)
	}
	schedule := domain.Schedule{Revision: "one", Programs: []domain.Program{{EpisodeID: "item", File: "a.mp4", Policy: domain.Stream, Start: time.Now().UTC(), Duration: 60}}}
	if err = d.SaveSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	if d.SaveSchedule(ctx, schedule) == nil {
		t.Fatal("schedule revision overwritten")
	}
	restored, err := d.Schedule(ctx)
	if err != nil || restored.Revision != "one" {
		t.Fatal("schedule persistence", err)
	}
	if err = d.NotModified(ctx, "item", "", ""); err != nil {
		t.Fatal(err)
	}
	var observations int
	if err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM observations WHERE status=304`).Scan(&observations); err != nil || observations != 1 {
		t.Fatal("304 history", err, observations)
	}
	if d.NotModified(ctx, "unknown", "", "") == nil {
		t.Fatal("unbacked 304 accepted")
	}
	if err = d.QueuePage(ctx, domain.DemocracyNow, 2, 2, []string{"item", "item", "other"}, true); err != nil {
		t.Fatal(err)
	}
	counts, err := d.Counts(ctx, domain.DemocracyNow)
	if err != nil || counts["pending"] != 2 {
		t.Fatal("discovery deduplication", err, counts)
	}
}
