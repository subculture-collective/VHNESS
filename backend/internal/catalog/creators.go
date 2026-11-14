package catalog

import (
	"context"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

// CatalogCreator is one person credited on visible items, with the number of
// items that credit them.
type CatalogCreator struct {
	Name  string `json:"name"`
	Items int64  `json:"items"`
}

// creatorFacet splits the credits selected by source (columns item, credit and
// rank) on commas and trims each name. Names that differ only in case count as
// one person, shown with the spelling of the lowest-ranked item.
func (d *DB) creatorFacet(ctx context.Context, source string, args ...any) ([]CatalogCreator, error) {
	rows, err := d.Pool.Query(ctx, `WITH credits AS (`+source+`), names AS (
 SELECT c.item,c.rank,n.ord,regexp_replace(n.name,'^\s+|\s+$','','g') AS name
 FROM credits c CROSS JOIN LATERAL regexp_split_to_table(c.credit,',') WITH ORDINALITY n(name,ord))
 SELECT (array_agg(name ORDER BY rank,ord))[1],count(DISTINCT item) FROM names WHERE name<>''
 GROUP BY lower(name) ORDER BY lower(name)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogCreator{}
	for rows.Next() {
		var c CatalogCreator
		if err = rows.Scan(&c.Name, &c.Items); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CatalogCreators lists the people credited on items visible in the audience
// scope. The kept spelling comes from the first item in the default catalog
// order (newest first).
func (d *DB) CatalogCreators(ctx context.Context, scope domain.CollectionScope, collection string) ([]CatalogCreator, error) {
	return d.creatorFacet(ctx, `SELECT item_id AS item,creator AS credit,row_number() OVER (ORDER BY date DESC,item_id) AS rank
 FROM (SELECT DISTINCT item_id,creator,date FROM `+d.catalogView()+` WHERE ($1::text[] IS NULL OR collection_id=ANY($1)) AND ($2='' OR collection_id=$2)) v`, []string(scope), collection)
}

// LegacyPrintCreators lists the people credited on published print documents,
// keeping the spelling of the first document in title order.
func (d *DB) LegacyPrintCreators(ctx context.Context, collection string) ([]CatalogCreator, error) {
	return d.creatorFacet(ctx, `SELECT i.id AS item,CASE WHEN jsonb_typeof(m->'creator')='string' THEN m->>'creator' ELSE '' END AS credit,
 row_number() OVER (ORDER BY lower(COALESCE(m->>'title','')),i.id) AS rank
 FROM catalog_items i CROSS JOIN LATERAL (SELECT (i.editorial->'legacy_document'->'metadata') || (i.editorial->'legacy_document'->'overrides') AS m) x
 WHERE i.editorial->'legacy_document'->>'published'='true' AND EXISTS(SELECT 1 FROM `+d.catalogView()+` v WHERE v.item_id=i.id AND v.collection_id=$1 AND v.media='print')`, collection)
}
