package catalog

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAutomaticPublicAccessIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; database integration unverified")
	}
	ctx := context.Background()
	admin, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Close()
	schema := "vhness_auto_test_" + time.Now().UTC().Format("20060102150405")
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE")
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
	operator := &DB{Pool: pool}
	public := &DB{Pool: pool, PublicOnly: true}
	if err = operator.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatal(err)
	}
	c := domain.Collection{ID: "auto-fixture", Policy: domain.Local, Budget: 1 << 20, Reserve: 1}
	if err = operator.Collection(ctx, c); err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		review   domain.TransportReview
		approval domain.PreservationApproval
	}
	fixtures := map[string]fixture{}
	add := func(id string, indexed bool) {
		t.Helper()
		raw := []byte(`{"fixture":"` + id + `","files":[{"name":"original.pdf"},{"name":"original_djvu.txt"}]}`)
		if err := operator.Snapshot(ctx, "source-"+id, raw, 200, "", "", nil); err != nil {
			t.Fatal(err)
		}
		rep := domain.Representation{ID: "pdf", Media: domain.PrintMedia, SourceItemID: "source-" + id, Snapshot: domain.Digest(raw), File: "original.pdf", Collections: []string{c.ID}, Extension: domain.MediaExtension{Print: &domain.PrintExtension{DocumentID: id, Variant: "original", Pages: 1, Companions: []domain.PrintCompanion{{File: "original_djvu.txt", Role: "ocr"}}}}}
		editorial := json.RawMessage(`{"legacy_document":{"published":true,"source_item":"source-` + id + `","source_key":"original","rights":"unknown","metadata":{"title":"Fixture ` + id + `"},"overrides":{}}}`)
		item := domain.CatalogItem{Editorial: editorial, ID: id, Title: "Fixture " + id, Date: "2026-10-03", Collections: []string{c.ID}, Representations: []domain.Representation{rep}}
		if err := operator.InsertCatalog(ctx, item, "Private mapping"); err != nil {
			t.Fatal(err)
		}
		content := []byte("synthetic automatic access fixture " + id)
		f := fixture{
			// Like retained migration inputs, the review also covers an OCR companion.
			review:   domain.TransportReview{Snapshot: rep.Snapshot, Files: []string{rep.File, "original_djvu.txt"}, Read: true, Download: true, Notes: "Private reading review"},
			approval: domain.PreservationApproval{Snapshot: rep.Snapshot, IndependentRecoverySHA256: domain.Digest([]byte("fixture restore")), Manifest: []domain.LocalOriginal{{File: rep.File, Bytes: int64(len(content)), SHA256: domain.Digest(content), Path: id + ".pdf"}, {File: "original_djvu.txt", Bytes: 4, SHA256: domain.Digest([]byte("text")), Path: id + ".txt"}}},
		}
		if err := operator.ReviewTransport(ctx, id, rep.ID, c.ID, f.review, f.approval); err != nil {
			t.Fatal(err)
		}
		if indexed {
			index := domain.PrintIndex{Snapshot: rep.Snapshot, OriginalSHA256: domain.Digest(content), Pages: 1, TextStatus: "page-text", Tool: "Synthetic fixture"}
			if err := operator.StorePrintIndex(ctx, id, rep.ID, rep.File, index, map[int]string{1: "fixture text"}); err != nil {
				t.Fatal(err)
			}
		}
		fixtures[id] = f
	}
	add("automatic", true)
	add("withdrawn", true)
	add("unindexed", false)
	add("manual", true)
	manual := fixtures["manual"].review
	manual.Public, manual.Notes = true, "Manual owner approval"
	if err = operator.ReviewPublicTransport(ctx, "manual", "pdf", c.ID, manual, fixtures["manual"].approval); err != nil {
		t.Fatal(err)
	}
	publicItems := func(want ...string) {
		t.Helper()
		page, err := public.Catalog(ctx, CatalogQuery{Page: 1, Collection: c.ID})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		docs, total, err := public.LegacyPrintDocuments(ctx, c.ID, "", "", "", "", 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		approved := 0
		for _, doc := range docs {
			if strings.Contains(string(doc), `"public_approved": true`) {
				approved++
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") || page.Total != len(want) || total != len(want) || approved != len(want) {
			t.Fatalf("public items %v (total %d, compatibility %d, approved %d), want %v", got, page.Total, total, approved, want)
		}
		for _, id := range []string{"automatic", "withdrawn", "unindexed", "manual"} {
			_, err := public.Transport(ctx, id, "pdf", c.ID)
			if covered := strings.Contains(","+strings.Join(want, ",")+",", ","+id+","); covered != (err == nil) {
				t.Fatalf("public transport for %s: %v", id, err)
			}
		}
	}
	reasons := func(r AutomaticAccessResult) map[string]string {
		m := map[string]string{}
		for _, s := range r.Skipped {
			m[s.Item] = s.Reason
		}
		return m
	}
	publicItems("manual")
	if _, err = operator.ReviewAutomaticPublicAccess(ctx, c.ID, true, " "); err == nil {
		t.Fatal("automatic access accepted without notes")
	}
	if _, err = operator.ReviewAutomaticPublicAccess(ctx, "missing", true, "Owner decision"); err == nil {
		t.Fatal("automatic access accepted an unknown collection")
	}

	result, err := operator.ReviewAutomaticPublicAccess(ctx, c.ID, true, "Owner decision: publish the collection for archival reading")
	if err != nil {
		t.Fatal(err)
	}
	if result.Approved != 2 || len(result.Skipped) != 1 || reasons(result)["unindexed"] != "primary file not reviewed or reader index incomplete" {
		t.Fatalf("automatic approval: %+v", result)
	}
	publicItems("automatic", "manual", "withdrawn")
	history, err := operator.TransportHistory(ctx, "automatic", "pdf", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Decision != "operator" || history[0].Public || history[1].Decision != "automatic" || !history[1].Public || history[1].AutomaticAccess == nil || *history[1].AutomaticAccess != result.Decision {
		t.Fatalf("automatic history: %+v", history)
	}
	var recorded bool
	if err = pool.QueryRow(ctx, `SELECT automatic FROM collection_public_access WHERE collection_id=$1`, c.ID).Scan(&recorded); err != nil || !recorded {
		t.Fatal("collection decision not recorded", err)
	}

	// Withdrawing one item uses the ordinary private review and is sticky.
	if err = operator.ReviewTransport(ctx, "withdrawn", "pdf", c.ID, fixtures["withdrawn"].review, fixtures["withdrawn"].approval); err != nil {
		t.Fatal(err)
	}
	publicItems("automatic", "manual")
	again, err := operator.ReviewAutomaticPublicAccess(ctx, c.ID, true, "Owner decision repeated after a new import")
	if err != nil {
		t.Fatal(err)
	}
	if again.Approved != 0 || reasons(again)["withdrawn"] != "explicit public withdrawal" {
		t.Fatalf("withdrawal not sticky: %+v", again)
	}
	publicItems("automatic", "manual")

	// Withdrawing the decision revokes only its own grants.
	withdrawal, err := operator.ReviewAutomaticPublicAccess(ctx, c.ID, false, "Owner withdrew automatic access")
	if err != nil {
		t.Fatal(err)
	}
	if withdrawal.Withdrawn != 1 || withdrawal.Approved != 0 {
		t.Fatalf("collection withdrawal: %+v", withdrawal)
	}
	publicItems("manual")
	history, err = operator.TransportHistory(ctx, "automatic", "pdf", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if last := history[len(history)-1]; last.Decision != "automatic" || last.Public || *last.AutomaticAccess != withdrawal.Decision || last.Review.Notes != "Owner withdrew automatic access" {
		t.Fatalf("withdrawal history: %+v", last)
	}

	// An automatic revocation is not an item withdrawal.
	restored, err := operator.ReviewAutomaticPublicAccess(ctx, c.ID, true, "Owner restored automatic access")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Approved != 1 {
		t.Fatalf("restored approval: %+v", restored)
	}
	publicItems("automatic", "manual")

	// A manual approval restores a withdrawn item.
	approved := fixtures["withdrawn"].review
	approved.Public, approved.Notes = true, "Manual owner approval after withdrawal"
	if err = operator.ReviewPublicTransport(ctx, "withdrawn", "pdf", c.ID, approved, fixtures["withdrawn"].approval); err != nil {
		t.Fatal(err)
	}
	publicItems("automatic", "manual", "withdrawn")
	var decisions int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM collection_public_access_history WHERE collection_id=$1`, c.ID).Scan(&decisions); err != nil || decisions != 4 {
		t.Fatal("decision history", decisions, err)
	}

	// A decision waits for a representation lock before it takes the collection
	// lock, so a rebind holding the representation can still lock the collection.
	if _, err = operator.ReviewAutomaticPublicAccess(ctx, c.ID, false, "Owner withdrew automatic access again"); err != nil {
		t.Fatal(err)
	}
	rebind, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rebind.Rollback(ctx)
	if _, err = rebind.Exec(ctx, `SET LOCAL lock_timeout='5s'; SELECT 1 FROM catalog_representations WHERE item_id='automatic' AND id='pdf' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result AutomaticAccessResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := operator.ReviewAutomaticPublicAccess(ctx, c.ID, true, "Owner restored automatic access during a rebind")
		done <- outcome{r, err}
	}()
	for waiting, tries := 0, 0; waiting == 0; tries++ {
		if tries > 100 {
			t.Fatal("automatic decision never waited for the representation lock")
		}
		time.Sleep(50 * time.Millisecond)
		if err = admin.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FOR UPDATE OF r%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = rebind.Exec(ctx, `SELECT 1 FROM collections WHERE id=$1 FOR UPDATE`, c.ID); err != nil {
		t.Fatal("collection lock behind a waiting automatic decision", err)
	}
	if err = rebind.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if o := <-done; o.err != nil || o.result.Approved != 1 {
		t.Fatalf("automatic decision after rebind: %+v %v", o.result, o.err)
	}
	publicItems("automatic", "manual", "withdrawn")
}
