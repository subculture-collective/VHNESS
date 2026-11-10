package sourcecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
)

var sampleSeed = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// SampleQuery selects a bounded, repeatable slice of the visible hash ring.
type SampleQuery struct {
	Seed  string
	Limit int
}

// Validate bounds the caller-supplied seed and the recording count.
func (q SampleQuery) Validate() error {
	if !sampleSeed.MatchString(q.Seed) || q.Limit < 0 || q.Limit > 50 {
		return fmt.Errorf("supply a URL-safe seed and a limit from 1 to 50")
	}
	return nil
}

// SamplePage intentionally has no continuation or expensive corpus status/total.
type SamplePage struct {
	Seed  string           `json:"seed"`
	Items []map[string]any `json:"items"`
}

// Sample traverses an indexed hash ring, wrapping once without a random full sort.
// It samples metadata only; playback permissions still require the resolver.
func (s *Store) Sample(ctx context.Context, q SampleQuery) (SamplePage, error) {
	page := SamplePage{Seed: q.Seed, Items: []map[string]any{}}
	if err := q.Validate(); err != nil {
		return page, err
	}
	if q.Limit == 0 {
		q.Limit = 12
	}
	hash := sha256.Sum256([]byte(q.Seed))
	anchor := hex.EncodeToString(hash[:16])
	var raw []byte
	err := s.Core.Pool.QueryRow(ctx, sampleSQL, s.Collection, anchor, q.Limit).Scan(&raw)
	if err != nil {
		return page, err
	}
	if err := json.Unmarshal(raw, &page.Items); err != nil {
		return page, err
	}
	return page, nil
}

// Kept as the exact production statement so the disposable plan check exercises it.
const sampleSQL = `WITH tail AS MATERIALIZED (
 SELECT identifier,effective,unavailable,source_type,md5(identifier) AS sample_key,0 AS wrapped
 FROM source_catalog_items WHERE collection_id=$1 AND NOT excluded AND md5(identifier)>=$2
 ORDER BY md5(identifier),identifier LIMIT $3
 ), head AS (
 SELECT identifier,effective,unavailable,source_type,md5(identifier) AS sample_key,1 AS wrapped
 FROM source_catalog_items WHERE collection_id=$1 AND NOT excluded AND md5(identifier)<$2
 ORDER BY md5(identifier),identifier LIMIT ($3-(SELECT count(*) FROM tail))
 ), selected AS (SELECT * FROM tail UNION ALL SELECT * FROM head)
 SELECT COALESCE(jsonb_agg(effective || jsonb_build_object('identifier',identifier,'unavailable',unavailable,'source_type',source_type)
 ORDER BY wrapped,sample_key,identifier),'[]'::jsonb) FROM selected`
