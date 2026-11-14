package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
)

type TransportEntry struct {
	Collection     domain.Collection
	Representation domain.Representation
	Raw            []byte
	Review         domain.TransportReview
	Preservation   domain.PreservationApproval
}

func (d *DB) Transport(ctx context.Context, item, representation, collection string) (TransportEntry, error) {
	var e TransportEntry
	var extension, review, approval []byte
	err := d.Pool.QueryRow(ctx, `
 SELECT c.id,c.policy,c.budget,c.reserve,c.source_item,r.id,r.media,r.source_item_id,r.source_hash,r.file,r.extension,s.raw,t.review,t.preservation
 FROM catalog_representations r
 JOIN snapshots s ON s.sha256=r.source_hash
 JOIN catalog_memberships m ON m.item_id=r.item_id AND m.collection_id=$3 AND m.listed
 JOIN catalog_representation_memberships rm ON rm.item_id=r.item_id AND rm.representation_id=r.id AND rm.collection_id=$3 AND rm.listed
 JOIN collections c ON c.id=$3
 JOIN representation_transport_reviews t ON t.item_id=r.item_id AND t.representation_id=r.id AND t.collection_id=$3
 WHERE r.item_id=$1 AND r.id=$2
 AND NOT EXISTS (SELECT 1 FROM source_catalog_items ci WHERE ci.collection_id=$3 AND ci.identifier=r.source_item_id AND (ci.unavailable OR ci.source_hash<>r.source_hash))
 AND (NOT $4 OR (t.review @> '{"public":true}'::jsonb AND t.review->>'snapshot_sha256'=r.source_hash))`, item, representation, collection, d.PublicOnly).Scan(&e.Collection.ID, &e.Collection.Policy, &e.Collection.Budget, &e.Collection.Reserve, &e.Collection.SourceItem, &e.Representation.ID, &e.Representation.Media, &e.Representation.SourceItemID, &e.Representation.Snapshot, &e.Representation.File, &extension, &e.Raw, &review, &approval)
	if err != nil {
		return e, err
	}
	e.Representation.Collections = []string{collection}
	if err = json.Unmarshal(extension, &e.Representation.Extension); err != nil {
		return e, err
	}
	if err = json.Unmarshal(review, &e.Review); err != nil {
		return e, err
	}
	err = json.Unmarshal(approval, &e.Preservation)
	return e, err
}

// ReviewTransport is operator-only. Catalog membership does not imply transport
// rights; each independently enabled action is checked against retained source
// identity, actual files and existing immutable collection policy.
func (d *DB) ReviewTransport(ctx context.Context, item, representation, collection string, review domain.TransportReview, approval domain.PreservationApproval) error {
	return d.reviewTransport(ctx, item, representation, collection, review, approval, false)
}

// ReviewPublicTransport requires an explicit operator approval path.
func (d *DB) ReviewPublicTransport(ctx context.Context, item, representation, collection string, review domain.TransportReview, approval domain.PreservationApproval) error {
	if !review.Public {
		return fmt.Errorf("public review requires explicit public approval")
	}
	return d.reviewTransport(ctx, item, representation, collection, review, approval, true)
}
func (d *DB) reviewTransport(ctx context.Context, item, representation, collection string, review domain.TransportReview, approval domain.PreservationApproval, public bool) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = reviewTransportTx(ctx, tx, item, representation, collection, review, approval, public); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func reviewTransportTx(ctx context.Context, tx pgx.Tx, item, representation, collection string, review domain.TransportReview, approval domain.PreservationApproval, public bool) error {
	return checkTransportTx(ctx, tx, item, representation, collection, review, approval, public, nil)
}

// checkTransportTx authorizes and records a review. A non-nil automatic links
// the review and its history row to a collection automatic access decision.
func checkTransportTx(ctx context.Context, tx pgx.Tx, item, representation, collection string, review domain.TransportReview, approval domain.PreservationApproval, public bool, automatic *int64) error {
	if strings.TrimSpace(review.Notes) == "" || (review.Public && !public) || len(review.Files) > 1000 {
		return fmt.Errorf("invalid transport review")
	}
	var err error
	var c domain.Collection
	var rep domain.Representation
	var extension, raw []byte
	err = tx.QueryRow(ctx, `SELECT c.id,c.policy,c.budget,c.reserve,c.source_item,r.id,r.media,r.source_item_id,r.source_hash,r.file,r.extension,s.raw FROM catalog_representations r JOIN snapshots s ON s.sha256=r.source_hash JOIN catalog_representation_memberships rm ON rm.item_id=r.item_id AND rm.representation_id=r.id AND rm.collection_id=$3 JOIN collections c ON c.id=$3 WHERE r.item_id=$1 AND r.id=$2 FOR UPDATE OF r,rm,c`, item, representation, collection).Scan(&c.ID, &c.Policy, &c.Budget, &c.Reserve, &c.SourceItem, &rep.ID, &rep.Media, &rep.SourceItemID, &rep.Snapshot, &rep.File, &extension, &raw)
	if err != nil {
		return err
	}
	rep.Collections = []string{collection}
	if err = json.Unmarshal(extension, &rep.Extension); err != nil {
		return err
	}
	if review.Snapshot != rep.Snapshot {
		return fmt.Errorf("review does not match current source snapshot")
	}
	actions := []struct {
		enabled bool
		action  domain.Action
	}{{review.Read, domain.ReadAction}, {review.Playback, domain.PlayAction}, {review.Download, domain.DownloadAction}, {review.Acquisition, domain.AcquireAction}, {review.Relay, domain.RelayAction}, {review.Schedule, domain.ScheduleAction}}
	for _, a := range actions {
		if a.enabled {
			if len(review.Files) == 0 {
				return fmt.Errorf("enabled transport needs exact files")
			}
			for _, file := range review.Files {
				if err = domain.AuthorizeTransport(c, rep, raw, review, approval, a.action, file); err != nil {
					return err
				}
			}
		}
	}
	return writeTransportTx(ctx, tx, item, representation, collection, review, approval, automatic)
}

func writeTransportTx(ctx context.Context, tx pgx.Tx, item, representation, collection string, review domain.TransportReview, approval domain.PreservationApproval, automatic *int64) error {
	reviewJSON, err := json.Marshal(review)
	if err != nil {
		return err
	}
	approvalJSON, err := json.Marshal(approval)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO representation_transport_reviews(item_id,representation_id,collection_id,review,preservation,automatic_access) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(item_id,representation_id,collection_id) DO UPDATE SET review=excluded.review,preservation=excluded.preservation,automatic_access=excluded.automatic_access,updated_at=now()`, item, representation, collection, reviewJSON, approvalJSON, automatic); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO representation_transport_history(item_id,representation_id,collection_id,review,preservation,automatic_access) VALUES($1,$2,$3,$4,$5,$6)`, item, representation, collection, reviewJSON, approvalJSON, automatic)
	return err
}
