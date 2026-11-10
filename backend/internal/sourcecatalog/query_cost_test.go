package sourcecatalog

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const spikeRows = 295994

// TestDiscoveryQueryCosts reproduces docs/reports/lineage-query-costs-2026-11-09.md
// against an explicitly disposable database. It only runs when requested: the
// generated corpus takes minutes and about 2 GB.
func TestDiscoveryQueryCosts(t *testing.T) {
	if os.Getenv("LINEAGE_QUERY_COST") == "" {
		t.Skip("set LINEAGE_QUERY_COST and a disposable TEST_DATABASE_URL")
	}
	s := testDB(t)
	ctx := context.Background()
	if err := s.Import(ctx, "synbase", fixture("synbase", "Room"), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := s.Core.Pool.QueryRow(ctx, `SELECT source_hash FROM source_catalog_items WHERE identifier='synbase'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	conn, err := s.Core.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	exec := func(q string, args ...any) {
		t0 := time.Now()
		if _, err := conn.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q[:60], err)
		}
		fmt.Printf("setup %.1fs %s\n", time.Since(t0).Seconds(), strings.Join(strings.Fields(q)[:min(3, len(strings.Fields(q)))], " "))
	}
	exec(`SELECT setseed(0.42)`)
	exec(`INSERT INTO source_catalog_items(collection_id,identifier,source_hash,normalized,normalization,effective,search)
 SELECT 'etree','syn'||lpad(i::text,6,'0'),$1,jsonb_build_object(
  'title','Band '||a||' Live at Venue '||v||' on '||d,'creator','Artist Number '||a,'date',d,'venue','Venue '||v,'coverage','City '||(v%900)||', ST',
  'description',(SELECT string_agg(md5((i*g)::text),' ') FROM generate_series(1,26) g),
  'taper','Taper '||(a%400),'transferer','Transfer '||(a%300),'source',(ARRAY['SBD > DAT','AUD: Schoeps > Nakamichi','Matrix','FM > cassette',''])[1+i%5],
  'lineage','DAT > WAV > FLAC '||md5(i::text),'subject','tag'||(a%50),'songs',(SELECT string_agg(left(md5((i+g)::text),8),' ') FROM generate_series(1,12) g)),
  '{"version":"live-music-v1","warnings":[],"derived":{}}','{}',''
 FROM (SELECT i,floor(power(12000::float8,random()))::int AS a,floor(40000*random())::int AS v,to_char(date '1965-01-01'+floor(random()*22280)::int,'YYYY-MM-DD') AS d FROM generate_series(1,$2::int) i) r`, hash, spikeRows)
	exec(`UPDATE source_catalog_items SET excluded=true WHERE identifier IN (SELECT identifier FROM source_catalog_items WHERE identifier LIKE 'syn%' ORDER BY identifier LIMIT 297)`)
	exec(`UPDATE source_catalog_items SET unavailable=true WHERE identifier IN (SELECT identifier FROM source_catalog_items WHERE identifier LIKE 'syn%' ORDER BY md5(identifier) LIMIT 1200)`)
	exec(`INSERT INTO source_catalog_audio_reviews(collection_id,item_id,source_hash,state,review)
 SELECT 'etree',identifier,$1,'reviewed',jsonb_build_object('files',jsonb_build_array('a.mp3')) FROM source_catalog_items WHERE identifier LIKE 'syn%' AND NOT excluded`, hash)
	exec(`INSERT INTO catalog_items(id,title) SELECT 'rec-'||identifier,'Show' FROM source_catalog_items WHERE identifier LIKE 'syn%' AND NOT excluded`)
	exec(`INSERT INTO catalog_memberships(item_id,collection_id,listed) SELECT 'rec-'||identifier,'etree',true FROM source_catalog_items WHERE identifier LIKE 'syn%' AND NOT excluded`)
	exec(`INSERT INTO catalog_representations(item_id,id,media,source_item_id,source_hash,file,extension) SELECT 'rec-'||identifier,'audio','audio',identifier,$1,'a.mp3','{}' FROM source_catalog_items WHERE identifier LIKE 'syn%' AND NOT excluded`, hash)
	exec(`INSERT INTO catalog_representation_memberships(item_id,representation_id,collection_id,listed) SELECT 'rec-'||identifier,'audio','etree',true FROM source_catalog_items WHERE identifier LIKE 'syn%' AND NOT excluded`)
	exec(`INSERT INTO representation_transport_reviews(item_id,representation_id,collection_id,review) SELECT 'rec-'||identifier,'audio','etree',jsonb_build_object('snapshot_sha256',$1::text,'playback','true','files',jsonb_build_array('a.mp3')) FROM source_catalog_items WHERE identifier LIKE 'syn%' AND NOT excluded`, hash)
	exec(`ANALYZE`)
	exec(`SELECT 1 FROM source_catalog_items LIMIT 1`)
	var count int64
	var size string
	_ = s.Core.Pool.QueryRow(ctx, `SELECT count(*),pg_size_pretty(pg_total_relation_size('source_catalog_items')) FROM source_catalog_items`).Scan(&count, &size)
	var nov int64
	_ = s.Core.Pool.QueryRow(ctx, `SELECT count(*) FROM source_catalog_items WHERE NOT excluded AND substring(effective->>'date',6,5)='11-09'`).Scan(&nov)
	fmt.Printf("corpus rows=%d size=%s anniversary=%d\n", count, size, nov)

	timed := func(name string, f func() error) {
		for i := 0; i < 3; i++ {
			t0 := time.Now()
			err := f()
			if err != nil {
				t.Fatal(name, err)
			}
			fmt.Printf("time %-28s run%d %8.1f ms\n", name, i+1, float64(time.Since(t0).Microseconds())/1000)
		}
	}
	searchOnly := func(q Query) func() error {
		return func() error {
			var total int64
			var raw []byte
			return s.Core.Pool.QueryRow(ctx, recordingSearchSQL, q.Search, q.Year, q.Artist, q.Venue, q.Availability, "", q.From, q.To, s.Collection, q.MonthDay, q.YearFrom, q.YearTo, q.Sort, "", q.Month, "").Scan(&total, &raw)
		}
	}
	home := Query{}
	day := Query{Sort: "date_desc", MonthDay: "11-09"}
	artist := Query{Artist: "Artist Number 1"}
	text := Query{Search: "live"}
	timed("Search H (with status)", func() error { _, err := s.Search(ctx, home); return err })
	timed("Search D (with status)", func() error { _, err := s.Search(ctx, day); return err })
	timed("Search artist (with status)", func() error { _, err := s.Search(ctx, artist); return err })
	timed("items SQL only H", searchOnly(home))
	timed("items SQL only D", searchOnly(day))
	timed("items SQL only artist", searchOnly(artist))
	timed("items SQL only text", searchOnly(text))
	timed("Status", func() error { _, err := s.Status(ctx); return err })
	timed("AudioCoverage", func() error { _, err := s.AudioCoverage(ctx); return err })
	timed("Facets", func() error { _, err := s.Facets(ctx); return err })
	timed("Calendar Nov all years", func() error { _, err := s.Calendar(ctx, CalendarQuery{Month: "11"}); return err })
	timed("Detail", func() error { _, err := s.Detail(ctx, "syn000500"); return err })

	withStatus, err := s.Search(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	var raw []byte
	if err := s.Core.Pool.QueryRow(ctx, recordingSearchSQL, "", "", "", "", "", "", "", "", s.Collection, "11-09", "", "", "date_desc", "", "", "").Scan(&total, &raw); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("equivalence total search=%d sqlonly=%d samepage=%d\n", withStatus.Total, total, len(withStatus.Items))
	source, _ := os.ReadFile("audio_batch.go")
	coverage := regexp.MustCompile("(?s)`(WITH reviews AS MATERIALIZED.*?)`").FindStringSubmatch(string(source))[1]
	plan := func(name, sql string, args ...any) {
		var lines []string
		rows, err := s.Core.Pool.Query(ctx, "EXPLAIN (ANALYZE,BUFFERS) "+sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		lines, err = pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("=== PLAN %s\n%s\n", name, strings.Join(lines, "\n"))
	}
	plan("items H", recordingSearchSQL, "", "", "", "", "", "", "", "", s.Collection, "", "", "", "", "", "", "")
	plan("items D", recordingSearchSQL, "", "", "", "", "", "", "", "", s.Collection, "11-09", "", "", "date_desc", "", "", "")
	plan("audio coverage", coverage, s.Collection)
}
