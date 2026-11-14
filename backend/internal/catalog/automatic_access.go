package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

type AutomaticAccessSkip struct {
	Item           string `json:"item"`
	Representation string `json:"representation"`
	Reason         string `json:"reason"`
}

type AutomaticAccessResult struct {
	Collection string                `json:"collection"`
	Automatic  bool                  `json:"automatic"`
	Decision   int64                 `json:"decision"`
	Approved   int                   `json:"approved"`
	Withdrawn  int                   `json:"withdrawn"`
	Skipped    []AutomaticAccessSkip `json:"skipped"`
}

// ReviewAutomaticPublicAccess records a collection-level automatic public
// access decision and applies it in the same transaction. Approval publishes
// listed print representations whose current private review already grants
// reading of the current source snapshot, including the primary file, and
// whose primary file has a reader index with at least one page.
// Each grant passes the ordinary transport checks and is linked to the
// decision. An operator review recorded after a public grant is an explicit
// withdrawal: later automatic decisions skip that representation until an
// operator approves it again. Withdrawing the decision revokes only the grants
// it made; manual public approvals stay in place.
func (d *DB) ReviewAutomaticPublicAccess(ctx context.Context, collection string, automatic bool, notes string) (AutomaticAccessResult, error) {
	result := AutomaticAccessResult{Collection: collection, Automatic: automatic, Skipped: []AutomaticAccessSkip{}}
	if strings.TrimSpace(notes) == "" || len(notes) > 4096 {
		return result, fmt.Errorf("automatic public access requires authorization notes")
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	// Lock representations before the collection, the order RebindRepresentation
	// and checkTransportTx use. Every transport review writer holds the
	// representation lock, so the candidate query below reads current reviews.
	if _, err = tx.Exec(ctx, `SELECT 1 FROM catalog_representations r JOIN catalog_representation_memberships rm ON rm.item_id=r.item_id AND rm.representation_id=r.id
 WHERE rm.collection_id=$1 AND r.media='print' ORDER BY r.item_id,r.id FOR UPDATE OF r`, collection); err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM collections WHERE id=$1 FOR UPDATE`, collection).Scan(&collection); err != nil {
		return result, fmt.Errorf("unknown collection: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO collection_public_access(collection_id,automatic,notes) VALUES($1,$2,$3) ON CONFLICT(collection_id) DO UPDATE SET automatic=excluded.automatic,notes=excluded.notes,updated_at=now()`, collection, automatic, notes); err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO collection_public_access_history(collection_id,automatic,notes) VALUES($1,$2,$3) RETURNING id`, collection, automatic, notes).Scan(&result.Decision); err != nil {
		return result, err
	}
	type candidate struct {
		item, representation, snapshot string
		review                         domain.TransportReview
		approval                       domain.PreservationApproval
		listed, withdrawn, stale, ok   bool
	}
	query := `SELECT t.item_id,t.representation_id,t.review,t.preservation,r.source_hash,false,false,false,false
 FROM representation_transport_reviews t JOIN catalog_representations r ON r.item_id=t.item_id AND r.id=t.representation_id
 WHERE t.collection_id=$1 AND t.automatic_access IS NOT NULL AND t.review @> '{"public":true}'::jsonb
 ORDER BY t.item_id,t.representation_id`
	if automatic {
		query = `SELECT t.item_id,t.representation_id,t.review,t.preservation,r.source_hash,
 m.listed AND rm.listed,
 EXISTS(SELECT 1 FROM representation_transport_history w WHERE w.item_id=t.item_id AND w.representation_id=t.representation_id AND w.collection_id=t.collection_id AND w.automatic_access IS NULL AND NOT w.review @> '{"public":true}'::jsonb
  AND EXISTS(SELECT 1 FROM representation_transport_history g WHERE g.item_id=w.item_id AND g.representation_id=w.representation_id AND g.collection_id=w.collection_id AND g.review @> '{"public":true}'::jsonb AND g.id<w.id)),
 EXISTS(SELECT 1 FROM source_catalog_items ci WHERE ci.collection_id=t.collection_id AND ci.identifier=r.source_item_id AND (ci.unavailable OR ci.source_hash<>r.source_hash)),
 t.review->'files' ? r.file AND EXISTS(SELECT 1 FROM print_indexes x WHERE x.item_id=t.item_id AND x.representation_id=t.representation_id AND x.file=r.file AND x.snapshot_sha256=r.source_hash AND x.pages>0 AND x.text_status<>'failed')
 FROM representation_transport_reviews t
 JOIN catalog_representations r ON r.item_id=t.item_id AND r.id=t.representation_id
 JOIN catalog_representation_memberships rm ON rm.item_id=t.item_id AND rm.representation_id=t.representation_id AND rm.collection_id=t.collection_id
 JOIN catalog_memberships m ON m.item_id=t.item_id AND m.collection_id=t.collection_id
 WHERE t.collection_id=$1 AND r.media='print' AND NOT t.review @> '{"public":true}'::jsonb
 ORDER BY t.item_id,t.representation_id`
	}
	rows, err := tx.Query(ctx, query, collection)
	if err != nil {
		return result, err
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		var review, approval []byte
		if err = rows.Scan(&c.item, &c.representation, &review, &approval, &c.snapshot, &c.listed, &c.withdrawn, &c.stale, &c.ok); err != nil {
			rows.Close()
			return result, err
		}
		if err = json.Unmarshal(review, &c.review); err == nil {
			err = json.Unmarshal(approval, &c.approval)
		}
		if err != nil {
			rows.Close()
			return result, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	decision := result.Decision
	for _, c := range candidates {
		c.review.Notes = notes
		if !automatic {
			// Revocation never needs authorization, so it writes directly.
			c.review.Public = false
			if err = writeTransportTx(ctx, tx, c.item, c.representation, collection, c.review, c.approval, &decision); err != nil {
				return result, err
			}
			result.Withdrawn++
			continue
		}
		reason := ""
		switch {
		case !c.listed:
			reason = "representation not listed"
		case c.withdrawn:
			reason = "explicit public withdrawal"
		case !c.review.Read:
			reason = "no reviewed reading access"
		case c.stale || c.review.Snapshot != c.snapshot:
			reason = "review does not match current source"
		case !c.ok:
			reason = "primary file not reviewed or reader index incomplete"
		}
		if reason == "" {
			c.review.Public = true
			point, e := tx.Begin(ctx)
			if e != nil {
				return result, e
			}
			if e = checkTransportTx(ctx, point, c.item, c.representation, collection, c.review, c.approval, true, &decision); e != nil {
				if err = point.Rollback(ctx); err != nil {
					return result, err
				}
				reason = "transport authorization failed: " + e.Error()
			} else if err = point.Commit(ctx); err != nil {
				return result, err
			}
		}
		if reason != "" {
			result.Skipped = append(result.Skipped, AutomaticAccessSkip{c.item, c.representation, reason})
			continue
		}
		result.Approved++
	}
	return result, tx.Commit(ctx)
}

type TransportHistoryEntry struct {
	ID              int64                  `json:"id"`
	Decision        string                 `json:"decision"`
	AutomaticAccess *int64                 `json:"automatic_access,omitempty"`
	Public          bool                   `json:"public"`
	Review          domain.TransportReview `json:"review"`
	CreatedAt       time.Time              `json:"created_at"`
}

// TransportHistory lists one representation's recorded transport reviews,
// oldest first. Decision is "automatic" for rows written by a collection
// automatic access decision and "operator" for ordinary reviews.
func (d *DB) TransportHistory(ctx context.Context, item, representation, collection string) ([]TransportHistoryEntry, error) {
	rows, err := d.Pool.Query(ctx, `SELECT id,automatic_access,review,created_at FROM representation_transport_history WHERE item_id=$1 AND representation_id=$2 AND collection_id=$3 ORDER BY id`, item, representation, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []TransportHistoryEntry{}
	for rows.Next() {
		var e TransportHistoryEntry
		var review []byte
		if err = rows.Scan(&e.ID, &e.AutomaticAccess, &review, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(review, &e.Review); err != nil {
			return nil, err
		}
		e.Public, e.Decision = e.Review.Public, "operator"
		if e.AutomaticAccess != nil {
			e.Decision = "automatic"
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
