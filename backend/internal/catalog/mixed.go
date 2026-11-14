package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

// InsertCatalog records reviewed identity and listing membership. It never
// upgrades delivery/acquisition decisions, nor overwrites another source item.
func (d *DB) InsertCatalog(ctx context.Context, item domain.CatalogItem, notes string) error {
	if err := item.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(notes) == "" {
		return fmt.Errorf("catalog mapping requires review notes")
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	editorial := item.Editorial
	if len(editorial) == 0 {
		editorial = json.RawMessage(`{}`)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_items(id,title,description,date,creator,editorial) VALUES($1,$2,$3,$4,$5,$6)`, item.ID, item.Title, item.Description, item.Date, item.Creator, editorial); err != nil {
		return err
	}
	for _, id := range item.Collections {
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_memberships(item_id,collection_id,listed) VALUES($1,$2,true)`, item.ID, id); err != nil {
			return err
		}
	}
	for _, rep := range item.Representations {
		// A digest alone cannot assert ownership of another item's source files.
		var sourceID string
		if err = tx.QueryRow(ctx, `SELECT item_id FROM snapshots WHERE sha256=$1`, rep.Snapshot).Scan(&sourceID); err != nil {
			return err
		}
		if sourceID != rep.SourceItemID {
			return fmt.Errorf("snapshot does not belong to representation source")
		}
		files := []string{rep.File}
		if rep.Extension.Print != nil {
			for _, companion := range rep.Extension.Print.Companions {
				files = append(files, companion.File)
			}
		}
		if rep.Extension.Audio != nil {
			for _, track := range rep.Extension.Audio.Tracks {
				files = append(files, track.File)
			}
			if recording := rep.Extension.Audio.Recording; recording != nil {
				for _, track := range recording.Tracks {
					for _, variant := range track.Variants {
						files = append(files, variant.Name)
					}
				}
			}
		}
		for _, file := range files {
			var present bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM snapshots s, jsonb_array_elements(s.parsed->'files') f WHERE s.sha256=$1 AND f->>'name'=$2 AND COALESCE(f->>'private','false') NOT IN ('true','1'))`, rep.Snapshot, file).Scan(&present); err != nil {
				return err
			}
			if !present {
				return fmt.Errorf("representation file absent or restricted in source snapshot")
			}
		}
		extension, e := json.Marshal(rep.Extension)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_representations(item_id,id,media,source_item_id,source_hash,file,extension) VALUES($1,$2,$3,$4,$5,$6,$7)`, item.ID, rep.ID, string(rep.Media), rep.SourceItemID, rep.Snapshot, rep.File, extension); err != nil {
			return err
		}
		for _, id := range rep.Collections {
			if _, err = tx.Exec(ctx, `INSERT INTO catalog_representation_memberships(item_id,representation_id,collection_id,listed) VALUES($1,$2,$3,true)`, item.ID, rep.ID, id); err != nil {
				return err
			}
		}
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_review_history(item_id,payload,notes) VALUES($1,$2,$3)`, item.ID, payload, notes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type CatalogQuery struct {
	Scope      []string // nil means unrestricted; an empty non-nil scope denies all.
	Collection string
	Media      domain.Media
	Search     string
	ID         string
	Page       int
	Creator    string
	DateFrom   string
	DateTo     string
	Label      string // accepted label only; a series label orders by issue position
	Text       string // indexed page text of the listed representations
}
type CatalogPage struct {
	Items          []domain.CatalogItem `json:"items"`
	Total          int                  `json:"total"`
	Page           int                  `json:"page"`
	SearchCoverage []string             `json:"search_coverage"`
}

type CatalogCollectionSummary struct {
	ID     string        `json:"id"`
	Title  string        `json:"title"`
	Policy domain.Policy `json:"policy"`
	Items  int64         `json:"items"`
	Media  []string      `json:"media"`
}

func (d *DB) CatalogCollections(ctx context.Context, scope domain.CollectionScope) ([]CatalogCollectionSummary, error) {
	rows, err := d.Pool.Query(ctx, `SELECT c.id,c.id,c.policy,count(DISTINCT v.item_id),array_agg(DISTINCT v.media ORDER BY v.media) FROM collections c JOIN `+d.catalogView()+` v ON v.collection_id=c.id WHERE ($1::text[] IS NULL OR c.id=ANY($1)) GROUP BY c.id ORDER BY c.id`, []string(scope))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogCollectionSummary{}
	for rows.Next() {
		var c CatalogCollectionSummary
		if err = rows.Scan(&c.ID, &c.Title, &c.Policy, &c.Items, &c.Media); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func ValidateCatalogFilters(creator, from, to string) error {
	if len(creator) > 200 {
		return fmt.Errorf("invalid creator filter")
	}
	for _, value := range []string{from, to} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return fmt.Errorf("invalid date filter")
			}
		}
	}
	if from != "" && to != "" && from > to {
		return fmt.Errorf("invalid date range")
	}
	return nil
}

func (d *DB) Catalog(ctx context.Context, q CatalogQuery) (CatalogPage, error) {
	p := CatalogPage{Items: []domain.CatalogItem{}, Page: q.Page, SearchCoverage: []string{"metadata"}}
	if q.Page < 1 || q.Page > 100000 || (q.Media != "" && !q.Media.Valid()) || len(q.Search) > 1000 || len(q.Text) > 200 || (q.Label != "" && !domain.ValidLabelID(q.Label)) {
		return p, fmt.Errorf("invalid catalog query")
	}
	if strings.TrimSpace(q.Text) != "" {
		p.SearchCoverage = []string{"metadata", "text"}
	}
	if err := ValidateCatalogFilters(q.Creator, q.DateFrom, q.DateTo); err != nil {
		return p, err
	}
	var raw []byte
	err := d.Pool.QueryRow(ctx, `
 WITH filtered AS MATERIALIZED (
 SELECT * FROM `+d.catalogView()+` v
 WHERE ($1::text[] IS NULL OR collection_id=ANY($1))
 AND ($2='' OR collection_id=$2) AND ($3='' OR media=$3)
 AND ($4='' OR position(lower($4) in lower(title||' '||description))>0)
 AND ($5='' OR item_id=$5)
 AND ($7='' OR position(lower($7) in lower(creator))>0)
 AND (($8='' AND $9='') OR (date ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' AND ($8='' OR date>=$8) AND ($9='' OR date<=$9)))
 AND ($10='' OR EXISTS(SELECT 1 FROM catalog_label_assignments a WHERE a.item_id=v.item_id AND a.label_id=$10 AND a.state='accepted'))
 AND (trim($11)='' OR EXISTS(SELECT 1 FROM print_page_text t JOIN print_indexes x ON x.item_id=t.item_id AND x.representation_id=t.representation_id AND x.file=t.file AND x.snapshot_sha256=v.source_hash WHERE t.item_id=v.item_id AND t.representation_id=v.representation_id AND t.search @@ plainto_tsquery('simple',$11)))
 ), keys AS (
 SELECT item_id,title,description,date,creator,
 CASE WHEN $10='' THEN NULL ELSE (SELECT a.issue_position FROM catalog_label_assignments a WHERE a.item_id=filtered.item_id AND a.label_id=$10 AND a.state='accepted') END AS issue_position
 FROM filtered GROUP BY item_id,title,description,date,creator
 ), selected AS (
 SELECT * FROM keys ORDER BY issue_position NULLS LAST,date DESC,item_id LIMIT 24 OFFSET $6
 ), result AS (
 SELECT s.*, (SELECT jsonb_agg(DISTINCT collection_id ORDER BY collection_id) FROM filtered f WHERE f.item_id=s.item_id) AS collections,
 (SELECT jsonb_agg(jsonb_build_object('id',r.representation_id,'media',r.media,'source_item_id',r.source_item_id,'snapshot_sha256',r.source_hash,'file',r.file,'extension',r.extension,'legacy_episode_id',r.legacy_episode_id,'collections',r.collections,'search_coverage',jsonb_build_array('metadata')) ORDER BY r.media,r.representation_id)
 FROM (SELECT representation_id,media,source_item_id,source_hash,file,extension,legacy_episode_id,jsonb_agg(DISTINCT collection_id ORDER BY collection_id) AS collections FROM filtered f WHERE f.item_id=s.item_id GROUP BY representation_id,media,source_item_id,source_hash,file,extension,legacy_episode_id) r) AS representations,
 (SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object('id',l.id,'kind',l.kind,'label',l.label,'issue_position',a.issue_position,'issue_label',a.issue_label)) ORDER BY l.kind,l.label) FROM catalog_label_assignments a JOIN catalog_labels l ON l.id=a.label_id WHERE a.item_id=s.item_id AND a.state='accepted') AS labels,
 CASE WHEN trim($11)='' THEN NULL ELSE (SELECT jsonb_agg(jsonb_build_object('representation',h.representation_id,'page',h.page,'snippet',h.snippet) ORDER BY h.representation_id,h.page) FROM (
  SELECT t.representation_id,t.page,ts_headline('simple',t.text,plainto_tsquery('simple',$11),'MaxFragments=1,MaxWords=30,MinWords=12,StartSel="",StopSel=""') AS snippet
  FROM print_page_text t JOIN print_indexes x ON x.item_id=t.item_id AND x.representation_id=t.representation_id AND x.file=t.file
  WHERE t.item_id=s.item_id AND t.search @@ plainto_tsquery('simple',$11)
  AND EXISTS(SELECT 1 FROM filtered f WHERE f.item_id=t.item_id AND f.representation_id=t.representation_id AND f.source_hash=x.snapshot_sha256)
  ORDER BY t.representation_id,t.page LIMIT 3) h) END AS text_hits
 FROM selected s
 ) SELECT (SELECT count(*) FROM keys),COALESCE(jsonb_agg(jsonb_build_object('id',item_id,'title',title,'description',description,'date',date,'creator',creator,'collections',collections,'representations',representations,'labels',labels,'text_hits',text_hits) ORDER BY issue_position NULLS LAST,date DESC,item_id),'[]'::jsonb) FROM result`, q.Scope, q.Collection, string(q.Media), q.Search, q.ID, (q.Page-1)*24, q.Creator, q.DateFrom, q.DateTo, q.Label, q.Text).Scan(&p.Total, &raw)
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p.Items)
	return p, err
}

func (d *DB) catalogView() string {
	if d.PublicOnly {
		return "public_catalog_representations"
	}
	return "visible_catalog_representations"
}

// CatalogView exposes the fixed audience-filtered view name to compatibility adapters.
func (d *DB) CatalogView() string { return d.catalogView() }
