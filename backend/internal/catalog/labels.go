package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

// ImportLabels upserts controlled vocabulary entries. A label's kind cannot
// change once assignments refer to it.
func (d *DB) ImportLabels(ctx context.Context, labels []domain.Label) error {
	if len(labels) == 0 || len(labels) > 10000 {
		return fmt.Errorf("label vocabulary must contain 1 to 10000 entries")
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, l := range labels {
		if err = l.Validate(); err != nil {
			return err
		}
		aliases, _ := json.Marshal(append([]string{}, l.Aliases...))
		var kind string
		err = tx.QueryRow(ctx, `SELECT kind FROM catalog_labels WHERE id=$1`, l.ID).Scan(&kind)
		if err == nil && kind != l.Kind {
			var used bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_label_assignments WHERE label_id=$1)`, l.ID).Scan(&used); err != nil {
				return err
			}
			if used {
				return fmt.Errorf("label %q kind cannot change while assigned", l.ID)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_labels(id,kind,label,aliases,description) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,label=excluded.label,aliases=excluded.aliases,description=excluded.description,updated_at=now()`, l.ID, l.Kind, l.Label, aliases, l.Description); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ProposeLabels records method proposals. Existing proposals are refreshed;
// accepted and rejected assignments keep their reviewed state and are not
// overwritten by a later run. A series proposal carries the item's issue
// position and issue label, which the review accepts with it.
func (d *DB) ProposeLabels(ctx context.Context, proposals []domain.LabelProposal, notes string) (int, error) {
	if strings.TrimSpace(notes) == "" || len(proposals) > 200000 {
		return 0, fmt.Errorf("label proposals require notes and at most 200000 entries")
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	changed := 0
	kinds := map[string]string{}
	for _, p := range proposals {
		if err = p.Validate(); err != nil {
			return 0, err
		}
		kind, known := kinds[p.Label]
		if !known {
			if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT kind FROM catalog_labels WHERE id=$1),'')`, p.Label).Scan(&kind); err != nil {
				return 0, err
			}
			kinds[p.Label] = kind
		}
		if kind == "" {
			return 0, fmt.Errorf("unknown label %q", p.Label)
		}
		if err = p.CheckKind(kind); err != nil {
			return 0, err
		}
		evidence := p.Evidence
		if len(evidence) == 0 {
			evidence = json.RawMessage(`{}`)
		}
		var issueLabel *string
		if p.IssuePosition != nil {
			issueLabel = &p.IssueLabel
		}
		tag, err := tx.Exec(ctx, `INSERT INTO catalog_label_assignments(item_id,label_id,state,method,score,evidence,issue_position,issue_label)
 SELECT $1,$2,'proposed',$3,$4,$5,$6,$7 WHERE EXISTS(SELECT 1 FROM catalog_items WHERE id=$1)
 ON CONFLICT(item_id,label_id) DO UPDATE SET method=excluded.method,score=excluded.score,evidence=excluded.evidence,issue_position=excluded.issue_position,issue_label=excluded.issue_label,updated_at=now()
 WHERE catalog_label_assignments.state='proposed'`, p.Item, p.Label, p.Method, p.Score, evidence, p.IssuePosition, issueLabel)
		if err != nil {
			return 0, err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_items WHERE id=$1)`, p.Item).Scan(&exists); err != nil {
				return 0, err
			}
			if !exists {
				return 0, fmt.Errorf("unknown item %q", p.Item)
			}
			continue // already reviewed
		}
		changed++
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_label_history(item_id,label_id,state,method,score,evidence,notes,issue_position,issue_label) VALUES($1,$2,'proposed',$3,$4,$5,$6,$7,$8)`, p.Item, p.Label, p.Method, p.Score, evidence, notes, p.IssuePosition, issueLabel); err != nil {
			return 0, err
		}
	}
	return changed, tx.Commit(ctx)
}

// ReviewLabels applies operator decisions to existing assignments.
func (d *DB) ReviewLabels(ctx context.Context, decisions []domain.LabelDecision, notes string) (int, error) {
	if strings.TrimSpace(notes) == "" || len(decisions) > 200000 {
		return 0, fmt.Errorf("label review requires notes and at most 200000 decisions")
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	for _, decision := range decisions {
		if err = decision.Validate(); err != nil {
			return 0, err
		}
		var method string
		var score float64
		var evidence []byte
		var position *int
		var issueLabel *string
		err = tx.QueryRow(ctx, `UPDATE catalog_label_assignments SET state=$3,notes=$4,updated_at=now() WHERE item_id=$1 AND label_id=$2 RETURNING method,score,evidence,issue_position,issue_label`, decision.Item, decision.Label, decision.State, notes).Scan(&method, &score, &evidence, &position, &issueLabel)
		if err != nil {
			return 0, fmt.Errorf("no assignment of %q to %q: %w", decision.Label, decision.Item, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_label_history(item_id,label_id,state,method,score,evidence,notes,issue_position,issue_label) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, decision.Item, decision.Label, decision.State, method, score, evidence, notes, position, issueLabel); err != nil {
			return 0, err
		}
	}
	return len(decisions), tx.Commit(ctx)
}

// CatalogLabelSummary is an accepted label with the number of visible items.
type CatalogLabelSummary struct {
	domain.ItemLabel
	Items int64 `json:"items"`
}

// CatalogLabels lists accepted labels on items visible in the audience scope.
func (d *DB) CatalogLabels(ctx context.Context, scope domain.CollectionScope, collection, kind string) ([]CatalogLabelSummary, error) {
	if kind != "" && !domain.LabelKinds[kind] {
		return nil, fmt.Errorf("invalid label kind")
	}
	rows, err := d.Pool.Query(ctx, `SELECT l.id,l.kind,l.label,count(DISTINCT a.item_id)
 FROM catalog_labels l JOIN catalog_label_assignments a ON a.label_id=l.id AND a.state='accepted'
 WHERE ($3='' OR l.kind=$3) AND EXISTS(SELECT 1 FROM `+d.catalogView()+` v WHERE v.item_id=a.item_id AND ($1::text[] IS NULL OR v.collection_id=ANY($1)) AND ($2='' OR v.collection_id=$2))
 GROUP BY l.id,l.kind,l.label ORDER BY l.kind,l.label`, []string(scope), collection, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogLabelSummary{}
	for rows.Next() {
		var s CatalogLabelSummary
		if err = rows.Scan(&s.ID, &s.Kind, &s.Label, &s.Items); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
