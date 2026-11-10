package sourcecatalog

import "context"

// ClassifyBatch recomputes a bounded number of unclassified records and reports
// how many remain. Writing a row runs the same projection as imports and
// corrections, so stopping between batches leaves pending rows reading unknown.
func (s *Store) ClassifyBatch(ctx context.Context, limit int) (classified, remaining int64, err error) {
	// A materialized CTE, because an IN subquery with SKIP LOCKED may be evaluated
	// repeatedly and exceed the limit.
	tag, err := s.Core.Pool.Exec(ctx, `WITH batch AS MATERIALIZED (
 SELECT identifier FROM source_catalog_items WHERE collection_id=$1 AND source_type_version=0
 ORDER BY identifier LIMIT $2 FOR UPDATE SKIP LOCKED)
 UPDATE source_catalog_items item SET updated_at=item.updated_at FROM batch
 WHERE item.collection_id=$1 AND item.identifier=batch.identifier`, s.Collection, limit)
	if err != nil {
		return 0, 0, err
	}
	classified = tag.RowsAffected()
	err = s.Core.Pool.QueryRow(ctx, `SELECT count(*) FROM source_catalog_items WHERE collection_id=$1 AND source_type_version=0`, s.Collection).Scan(&remaining)
	return classified, remaining, err
}
