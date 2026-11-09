// Package sourcecatalog indexes retained source metadata without granting media
// access. It uses the shared engine's database, snapshots and operator boundary.
package sourcecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	Core       *catalog.DB
	Collection string
}

func (s *Store) Init(ctx context.Context) error {
	if !domain.ValidID(s.Collection) {
		return fmt.Errorf("invalid collection")
	}
	if _, err := s.Core.GetCollection(ctx, s.Collection); err != nil {
		return err
	}
	_, err := s.Core.Pool.Exec(ctx, `INSERT INTO source_catalog_worker_state(collection_id) VALUES($1) ON CONFLICT DO NOTHING`, s.Collection)
	return err
}
func (s *Store) Import(ctx context.Context, id string, raw []byte, status int, etag, modified string) error {
	normalized, rules, err := Normalize(s.Collection, id, raw)
	if err != nil {
		return err
	}
	if err = s.Core.Snapshot(ctx, id, raw, status, etag, modified, nil); err != nil {
		return err
	}
	projection, _ := json.Marshal(normalized)
	provenance, _ := json.Marshal(rules)
	hash := domain.Digest(raw)
	tx, err := s.Core.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO source_catalog_items(collection_id,identifier,source_hash,normalized,normalization,effective,search) VALUES($1,$2,$3,$4,$5,'{}','') ON CONFLICT(collection_id,identifier) DO UPDATE SET source_hash=excluded.source_hash,normalized=excluded.normalized,normalization=excluded.normalization,unavailable=false,updated_at=now()`, s.Collection, id, hash, projection, provenance); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO source_catalog_queue(collection_id,item_id,state) VALUES($1,$2,'imported') ON CONFLICT(collection_id,item_id) DO UPDATE SET state='imported',attempts=source_catalog_queue.attempts+1,error='',updated_at=now()`, s.Collection, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Edit(ctx context.Context, id string, patch map[string]any, notes string) error {
	if !domain.ValidID(id) {
		return fmt.Errorf("invalid source identity")
	}
	if err := ValidatePatch(patch, notes); err != nil {
		return err
	}
	tx, err := s.Core.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT editorial FROM source_catalog_items WHERE collection_id=$1 AND identifier=$2 FOR UPDATE`, s.Collection, id).Scan(&raw); err != nil {
		return err
	}
	edits := map[string]any{}
	if err = json.Unmarshal(raw, &edits); err != nil {
		return err
	}
	for key, value := range patch {
		if value == nil {
			delete(edits, key)
		} else {
			edits[key] = strings.TrimSpace(value.(string))
		}
	}
	current, _ := json.Marshal(edits)
	change, _ := json.Marshal(patch)
	if _, err = tx.Exec(ctx, `UPDATE source_catalog_items SET editorial=$3,updated_at=now() WHERE collection_id=$1 AND identifier=$2`, s.Collection, id, current); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO source_catalog_history(collection_id,item_id,patch,notes) VALUES($1,$2,$3,$4)`, s.Collection, id, change, notes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type Query struct{ Search, Year, Artist, Venue, Availability, Cursor, From, To, Collection, MonthDay, YearFrom, YearTo, Sort, Month string }

func (q Query) Validate() error {
	if q.Month != "" && !regexpMonth.MatchString(q.Month) {
		return fmt.Errorf("use a month in MM form")
	}
	if err := validateDiscovery(q.MonthDay, q.YearFrom, q.YearTo); err != nil {
		return err
	}
	if q.Sort != "" && q.Sort != "date_desc" && q.Sort != "date_asc" && q.Sort != "artist_asc" {
		return fmt.Errorf("invalid recording order")
	}
	if len(q.Search) > 300 || len(q.Artist) > 300 || len(q.Venue) > 300 || len(q.Cursor) > 1024 {
		return fmt.Errorf("search or filter is too long")
	}
	if q.Year != "" && !Year.MatchString(q.Year) {
		return fmt.Errorf("use a four-digit year")
	}
	if q.Availability != "" && q.Availability != "available" && q.Availability != "unavailable" {
		return fmt.Errorf("invalid availability filter")
	}
	for _, date := range []string{q.From, q.To} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return fmt.Errorf("use complete YYYY-MM-DD dates")
			}
		}
	}
	if q.From != "" && q.To != "" && q.From > q.To {
		return fmt.Errorf("date range is reversed")
	}
	return nil
}

type pageCursor struct {
	After  string `json:"after"`
	Filter string `json:"filter"`
	Date   string `json:"date,omitempty"`
	Artist string `json:"artist,omitempty"`
}

func (q Query) fingerprint() string {
	q.Cursor = ""
	raw, _ := json.Marshal(q)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (q Query) after() (string, error) {
	if q.Cursor == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	var c pageCursor
	if err != nil || json.Unmarshal(raw, &c) != nil || !ItemID.MatchString(c.After) || c.Filter != q.fingerprint() || ((q.Sort == "date_desc" || q.Sort == "date_asc") && !validCompleteDate(c.Date)) {
		return "", fmt.Errorf("invalid cursor or changed filters")
	}
	return c.After, nil
}

type Page struct {
	Items  []map[string]any `json:"items"`
	Total  int64            `json:"total"`
	Cursor string           `json:"cursor,omitempty"`
	Status Status           `json:"status"`
}

func (s *Store) Search(ctx context.Context, q Query) (Page, error) {
	q.Collection = s.Collection
	page := Page{Items: []map[string]any{}}
	if err := q.Validate(); err != nil {
		return page, err
	}
	after, err := q.after()
	if err != nil {
		return page, err
	}
	var raw []byte
	afterDate, afterArtist := "", ""
	if q.Cursor != "" {
		decoded, _ := base64.RawURLEncoding.DecodeString(q.Cursor)
		var cursor pageCursor
		if err := json.Unmarshal(decoded, &cursor); err != nil {
			return page, err
		}
		afterDate, afterArtist = cursor.Date, cursor.Artist
	}
	err = s.Core.Pool.QueryRow(ctx, recordingSearchSQL, q.Search, q.Year, q.Artist, q.Venue, q.Availability, after, q.From, q.To, s.Collection, q.MonthDay, q.YearFrom, q.YearTo, q.Sort, afterDate, q.Month, afterArtist).Scan(&page.Total, &raw)
	if err != nil {
		return page, err
	}
	if err = json.Unmarshal(raw, &page.Items); err != nil {
		return page, err
	}
	if len(page.Items) > 100 {
		page.Items = page.Items[:100]
		cursorDate := ""
		if q.Sort == "date_desc" || q.Sort == "date_asc" {
			cursorDate, _ = page.Items[99]["date"].(string)
		}
		cursorArtist := ""
		if q.Sort == "artist_asc" {
			cursorArtist, _ = page.Items[99]["_cursor_artist"].(string)
		}
		cursor, _ := json.Marshal(pageCursor{After: page.Items[99]["identifier"].(string), Filter: q.fingerprint(), Date: cursorDate, Artist: cursorArtist})
		page.Cursor = base64.RawURLEncoding.EncodeToString(cursor)
	}
	for _, item := range page.Items {
		delete(item, "_cursor_artist")
	}
	page.Status, err = s.Status(ctx)
	return page, err
}
func (s *Store) Detail(ctx context.Context, id string) (map[string]any, error) {
	var source, effective, normalized, editorial, rules, history []byte
	var hash string
	var updated time.Time
	var unavailable, excluded bool
	err := s.Core.Pool.QueryRow(ctx, `SELECT s.parsed,r.effective,r.normalized,r.editorial,r.normalization,r.source_hash,r.updated_at,r.unavailable,r.excluded,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('patch',h.patch,'notes',h.notes,'created_at',h.created_at) ORDER BY h.id DESC) FROM (SELECT * FROM source_catalog_history WHERE collection_id=r.collection_id AND item_id=r.identifier ORDER BY id DESC LIMIT 20) h),'[]'::jsonb)
 FROM source_catalog_items r JOIN snapshots s ON s.sha256=r.source_hash WHERE r.collection_id=$1 AND r.identifier=$2`, s.Collection, id).Scan(&source, &effective, &normalized, &editorial, &rules, &hash, &updated, &unavailable, &excluded, &history)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err = json.Unmarshal(source, &result); err != nil {
		return nil, err
	}
	result["source_metadata"] = result["metadata"]
	result["metadata"] = json.RawMessage(effective)
	result["normalized"] = json.RawMessage(normalized)
	result["editorial"] = json.RawMessage(editorial)
	result["normalization"] = json.RawMessage(rules)
	result["history"] = json.RawMessage(history)
	result["snapshot_sha256"] = hash
	result["updated_at"] = updated
	result["unavailable"] = unavailable
	result["excluded"] = excluded
	var bindings []byte
	err = s.Core.Pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('item',r.item_id,'representation',r.id,'files',t.review->'files')),'[]'::jsonb)
 FROM catalog_representations r JOIN representation_transport_reviews t ON t.item_id=r.item_id AND t.representation_id=r.id AND t.collection_id=$1
 JOIN catalog_representation_memberships m ON m.item_id=r.item_id AND m.representation_id=r.id AND m.collection_id=$1 AND m.listed
 JOIN catalog_memberships cm ON cm.item_id=r.item_id AND cm.collection_id=$1 AND cm.listed
 WHERE r.source_item_id=$2 AND r.source_hash=$3 AND t.review->>'snapshot_sha256'=$3 AND t.review->>'playback'='true'`, s.Collection, id, hash).Scan(&bindings)
	if err != nil {
		return nil, err
	}
	result["playback_bindings"] = json.RawMessage(bindings)
	return result, nil
}
func (s *Store) Raw(ctx context.Context, id string) ([]byte, error) {
	var raw []byte
	err := s.Core.Pool.QueryRow(ctx, `SELECT s.raw FROM snapshots s JOIN source_catalog_items r ON r.source_hash=s.sha256 WHERE r.collection_id=$1 AND r.identifier=$2`, s.Collection, id).Scan(&raw)
	return raw, err
}

type Status struct {
	Excluded          int64         `json:"excluded"`
	Playback          AudioCoverage `json:"playback"`
	RetryAt           *time.Time    `json:"retry_at,omitempty"`
	LastError         string        `json:"last_error,omitempty"`
	Imported          int64         `json:"imported"`
	Discovered        int64         `json:"discovered"`
	Pending           int64         `json:"pending"`
	Failed            int64         `json:"failed"`
	DiscoveryComplete bool          `json:"discovery_complete"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	var status Status
	err := s.Core.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM source_catalog_items WHERE collection_id=$1),discovered,
 (SELECT count(*) FROM source_catalog_queue WHERE collection_id=$1 AND state IN ('pending','retry')),
 (SELECT count(*) FROM source_catalog_queue WHERE collection_id=$1 AND state IN ('rejected','unavailable')),complete,updated_at,retry_at,last_error,(SELECT count(*) FROM source_catalog_items WHERE collection_id=$1 AND excluded) FROM source_catalog_worker_state WHERE collection_id=$1`, s.Collection).Scan(&status.Imported, &status.Discovered, &status.Pending, &status.Failed, &status.DiscoveryComplete, &status.UpdatedAt, &status.RetryAt, &status.LastError, &status.Excluded)
	if err == nil {
		status.Playback, err = s.AudioCoverage(ctx)
	}
	return status, err
}

type Facet struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

func (s *Store) Facets(ctx context.Context) (map[string][]Facet, error) {
	result := map[string][]Facet{}
	for _, name := range []string{"artists", "years", "venues"} {
		field := "effective->>'creator'"
		if name == "years" {
			field = "left(effective->>'date',4)"
		}
		if name == "venues" {
			field = "effective->>'venue'"
		}
		rows, err := s.Core.Pool.Query(ctx, `SELECT `+field+`,count(*) FROM source_catalog_items WHERE collection_id=$1 AND NOT excluded AND `+field+`<>'' GROUP BY 1 ORDER BY count(*) DESC,1 LIMIT 200`, s.Collection)
		if err != nil {
			return nil, err
		}
		values, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Facet, error) {
			var f Facet
			err := row.Scan(&f.Value, &f.Count)
			return f, err
		})
		if err != nil {
			return nil, err
		}
		sort.Slice(values, func(i, j int) bool { return values[i].Value < values[j].Value })
		result[name] = values
	}
	return result, nil
}

func ValidateCursor(q Query) error { _, err := q.after(); return err }

// Shared with the disposable query-plan inspection.
const recordingSearchSQL = `WITH filtered AS MATERIALIZED (
 SELECT identifier,effective,unavailable FROM source_catalog_items WHERE collection_id=$9 AND NOT excluded AND
 ($1='' OR search @@ websearch_to_tsquery('simple',$1))
 AND ($2='' OR left(effective->>'date',4)=$2)
 AND ($3='' OR lower(effective->>'creator')=lower($3))
 AND ($4='' OR position(lower($4) in lower(effective->>'venue'))>0)
 AND ($5='' OR ($5='available' AND NOT unavailable) OR ($5='unavailable' AND unavailable))
 AND ($10='' OR (source_catalog_complete_date(effective->>'date') AND substring(effective->>'date',6,5)=$10))
 AND ($11='' OR left(effective->>'date',4)>=$11) AND ($12='' OR left(effective->>'date',4)<=$12)
 AND ($13 NOT IN ('date_desc','date_asc') OR source_catalog_complete_date(effective->>'date'))
 AND ($15='' OR (source_catalog_complete_date(effective->>'date') AND substring(effective->>'date',6,2)=$15))
 AND (($7='' AND $8='') OR (source_catalog_complete_date(effective->>'date') AND ($7='' OR effective->>'date'>=$7) AND ($8='' OR effective->>'date'<=$8)))
 ), selected AS (SELECT * FROM filtered WHERE
 (($13='' AND identifier>$6)
 OR ($13='date_desc' AND ($6='' OR effective->>'date'<$14 OR (effective->>'date'=$14 AND identifier>$6)))
 OR ($13='date_asc' AND ($6='' OR effective->>'date'>$14 OR (effective->>'date'=$14 AND identifier>$6)))
 OR ($13='artist_asc' AND ($6='' OR lower(effective->>'creator')>$16 OR (lower(effective->>'creator')=$16 AND identifier>$6))))
 ORDER BY CASE WHEN $13='date_desc' THEN effective->>'date' END DESC,CASE WHEN $13='date_asc' THEN effective->>'date' END ASC,CASE WHEN $13='artist_asc' THEN lower(effective->>'creator') END ASC,identifier LIMIT 101)
 SELECT (SELECT count(*) FROM filtered),COALESCE(jsonb_agg(effective || jsonb_build_object('identifier',identifier,'unavailable',unavailable,'_cursor_artist',lower(effective->>'creator')) ORDER BY CASE WHEN $13='date_desc' THEN effective->>'date' END DESC,CASE WHEN $13='date_asc' THEN effective->>'date' END ASC,CASE WHEN $13='artist_asc' THEN lower(effective->>'creator') END ASC,identifier),'[]'::jsonb) FROM selected`
