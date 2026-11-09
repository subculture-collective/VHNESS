package sourcecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// DirectoryQuery selects a complete collection-scoped directory, not top facets.
type DirectoryQuery struct {
	Collection, Dimension, Search, Sort, Cursor string
	Limit                                       int
}

func (q DirectoryQuery) defaults() DirectoryQuery {
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Sort == "" {
		q.Sort = "count_desc"
		if q.Dimension == "years" {
			q.Sort = "name_desc"
		}
	}
	return q
}

type directoryCursor struct {
	Value       string `json:"value"`
	Count       int64  `json:"count"`
	Fingerprint string `json:"filter"`
}

func (q DirectoryQuery) fingerprint() string {
	q = q.defaults()
	q.Cursor = ""
	raw, _ := json.Marshal(q)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (q DirectoryQuery) after() (directoryCursor, error) {
	var c directoryCursor
	if q.Cursor == "" {
		return c, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.Value == "" || c.Count < 1 || c.Fingerprint != q.fingerprint() {
		return c, fmt.Errorf("invalid directory cursor or changed query")
	}
	return c, nil
}

// Validate checks bounded directory inputs, including the cursor's query binding.
func (q DirectoryQuery) Validate() error {
	q = q.defaults()
	if q.Dimension != "artists" && q.Dimension != "venues" && q.Dimension != "years" {
		return fmt.Errorf("choose artists, venues, or years")
	}
	if q.Sort != "count_desc" && q.Sort != "name_asc" && q.Sort != "name_desc" {
		return fmt.Errorf("invalid directory order")
	}
	if q.Limit < 1 || q.Limit > 100 || len(q.Search) > 300 || len(q.Cursor) > 8192 {
		return fmt.Errorf("directory query exceeds its bounds")
	}
	_, err := q.after()
	return err
}

// DirectoryPage counts distinct matching entries across all visible recordings.
type DirectoryPage struct {
	Dimension string  `json:"dimension"`
	Search    string  `json:"q"`
	Sort      string  `json:"sort"`
	Items     []Facet `json:"items"`
	Total     int64   `json:"total"`
	Cursor    string  `json:"cursor,omitempty"`
}

// Directory returns one bounded keyset page of a complete directory.
func (s *Store) Directory(ctx context.Context, q DirectoryQuery) (DirectoryPage, error) {
	q.Collection = s.Collection
	q = q.defaults()
	page := DirectoryPage{Dimension: q.Dimension, Search: q.Search, Sort: q.Sort, Items: []Facet{}}
	if err := q.Validate(); err != nil {
		return page, err
	}
	c, err := q.after()
	if err != nil {
		return page, err
	}
	field, dateClause := "effective->>'creator'", ""
	if q.Dimension == "venues" {
		field = "effective->>'venue'"
	} else if q.Dimension == "years" {
		field = "left(effective->>'date',4)"
		dateClause = " AND source_catalog_complete_date(effective->>'date')"
	}
	order, after := "count DESC,value ASC", "(count<$4 OR (count=$4 AND value>$3))"
	if q.Sort == "name_asc" {
		order, after = "value ASC", "value>$3"
	} else if q.Sort == "name_desc" {
		order, after = "value DESC", "value<$3"
	}
	// Field, order and predicate come only from the validated cases above.
	query := `WITH grouped AS MATERIALIZED (
 SELECT ` + field + ` AS value,count(*) AS count FROM source_catalog_items
 WHERE collection_id=$1 AND NOT excluded AND ` + field + `<>''
 AND position(lower($2) in lower(` + field + `))>0` + dateClause + ` GROUP BY 1
 ), selected AS (SELECT * FROM grouped WHERE $3='' OR ($4::bigint>=0 AND ` + after + `)
 ORDER BY ` + order + ` LIMIT $5)
 SELECT (SELECT count(*) FROM grouped),COALESCE(jsonb_agg(jsonb_build_object('value',value,'count',count)
 ORDER BY ` + order + `),'[]'::jsonb) FROM selected`
	var raw []byte
	if err := s.Core.Pool.QueryRow(ctx, query, s.Collection, q.Search, c.Value, c.Count, q.Limit+1).Scan(&page.Total, &raw); err != nil {
		return page, err
	}
	if err := json.Unmarshal(raw, &page.Items); err != nil {
		return page, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[q.Limit-1]
		raw, err := json.Marshal(directoryCursor{Value: last.Value, Count: last.Count, Fingerprint: q.fingerprint()})
		if err != nil {
			return page, err
		}
		page.Cursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}
