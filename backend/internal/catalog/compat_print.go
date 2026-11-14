package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

// LegacyPrintDocuments projects the retained print contract from native mappings.
// Current audience/membership views select the variants; retained source and
// editorial fields supply the same labels and rights text as the original reader.
// Series is an accepted series label ID; it orders documents by issue position.
type PrintBrowseFilters struct{ Creator, DateFrom, DateTo, Series string }

func (f PrintBrowseFilters) Validate() error {
	if len(f.Creator) > 200 {
		return fmt.Errorf("creator exceeds 200 characters")
	}
	if f.Series != "" && !domain.ValidLabelID(f.Series) {
		return fmt.Errorf("invalid series")
	}
	for _, value := range []string{f.DateFrom, f.DateTo} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return fmt.Errorf("use complete YYYY-MM-DD date bounds")
			}
		}
	}
	if f.DateFrom != "" && f.DateTo != "" && f.DateFrom > f.DateTo {
		return fmt.Errorf("the original-date range is reversed")
	}
	return nil
}

func (d *DB) LegacyPrintDocuments(ctx context.Context, collection, id, q, subject, language string, limit, offset int, filters ...PrintBrowseFilters) ([]json.RawMessage, int, error) {
	var filter PrintBrowseFilters
	if len(filters) > 0 {
		filter = filters[0]
	}
	if err := filter.Validate(); err != nil {
		return nil, 0, err
	}
	if limit < 1 || limit > 50 || offset < 0 || offset > 100000 {
		return nil, 0, fmt.Errorf("invalid print page")
	}
	const metadata = `(i.editorial->'legacy_document'->'metadata') || (i.editorial->'legacy_document'->'overrides')`
	publicMetadata := `jsonb_build_object('title',(` + metadata + `)->'title','creator',(` + metadata + `)->'creator','date',(` + metadata + `)->'date','language',(` + metadata + `)->'language','subjects',(` + metadata + `)->'subjects')`
	effective := `CASE WHEN $6 THEN ` + publicMetadata + ` ELSE ` + metadata + ` END`
	where := ` FROM catalog_items i WHERE i.editorial->'legacy_document'->>'published'='true' AND ($2='' OR i.id=$2) AND EXISTS(SELECT 1 FROM ` + d.catalogView() + ` v WHERE v.item_id=i.id AND v.media='print' AND v.collection_id=$1)
 AND ($4='' OR (` + metadata + `)->'subjects' ? $4) AND ($5='' OR (` + metadata + `)->>'language'=$5)
 AND ($3='' OR to_tsvector('simple',(` + effective + `)::text) @@ plainto_tsquery('simple',$3) OR EXISTS(SELECT 1 FROM print_page_text p JOIN ` + d.catalogView() + ` v ON v.item_id=p.item_id AND v.representation_id=p.representation_id AND v.file=p.file WHERE v.item_id=i.id AND v.media='print' AND v.collection_id=$1 AND p.search @@ plainto_tsquery('simple',$3)))`
	const originalDate = `i.editorial->'legacy_document'->'metadata'->>'date'`
	where += ` AND ($7='' OR position(lower($7) in lower(COALESCE((` + metadata + `)->>'creator','')))>0) AND ($8='' OR (` + originalDate + ` ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' AND ` + originalDate + ` >= $8)) AND ($9='' OR (` + originalDate + ` ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' AND ` + originalDate + ` <= $9))`
	where += ` AND ($10='' OR EXISTS(SELECT 1 FROM catalog_label_assignments a JOIN catalog_labels l ON l.id=a.label_id WHERE a.item_id=i.id AND a.label_id=$10 AND a.state='accepted' AND l.kind='series'))`
	const issueOrder = `CASE WHEN $10='' THEN NULL ELSE (SELECT a.issue_position FROM catalog_label_assignments a WHERE a.item_id=i.id AND a.label_id=$10 AND a.state='accepted') END`
	const series = `COALESCE((SELECT jsonb_agg(jsonb_build_object('slug',l.id,'name',l.label,'issue_position',a.issue_position,'issue_label',a.issue_label) ORDER BY l.label,l.id) FROM catalog_label_assignments a JOIN catalog_labels l ON l.id=a.label_id WHERE a.item_id=i.id AND a.state='accepted' AND l.kind='series'),'[]'::jsonb)`
	args := []any{collection, id, q, subject, language, d.PublicOnly, filter.Creator, filter.DateFrom, filter.DateTo, filter.Series}
	var total int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*)`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.Pool.Query(ctx, `SELECT jsonb_build_object('id',i.id,'collection',$1::text,'source_item',i.editorial->'legacy_document'->'source_item','key',i.editorial->'legacy_document'->'source_key','original_date',`+originalDate+`,'metadata',`+effective+`,'series',`+series+`,'published',true,'rights',i.editorial->'legacy_document'->'rights','variants',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',v.representation_id,'filename',v.file,'sha256',original->>'sha256','size',(original->>'bytes')::bigint,'pages',COALESCE(x.pages,0),'text_status',COALESCE(x.text_status,'pending'),'public_approved',COALESCE(t.review->>'public','false')='true') ORDER BY v.file,v.representation_id) FROM `+d.catalogView()+` v LEFT JOIN representation_transport_reviews t ON t.item_id=v.item_id AND t.representation_id=v.representation_id AND t.collection_id=v.collection_id LEFT JOIN LATERAL jsonb_array_elements(t.preservation->'manifest') original ON original->>'file'=v.file LEFT JOIN print_indexes x ON x.item_id=v.item_id AND x.representation_id=v.representation_id AND x.file=v.file WHERE v.item_id=i.id AND v.media='print' AND v.collection_id=$1),'[]'::jsonb),'hits',COALESCE((SELECT jsonb_agg(h.hit ORDER BY h.page,h.rep) FROM (SELECT p.page,p.representation_id rep,jsonb_build_object('variant',p.representation_id,'page',p.page,'snippet',left(p.text,350)) hit FROM print_page_text p JOIN `+d.catalogView()+` v ON v.item_id=p.item_id AND v.representation_id=p.representation_id AND v.file=p.file WHERE v.item_id=i.id AND v.media='print' AND v.collection_id=$1 AND $3<>'' AND p.search @@ plainto_tsquery('simple',$3) ORDER BY p.page,p.representation_id LIMIT 5) h),'[]'::jsonb))`+where+` ORDER BY `+issueOrder+` NULLS LAST,lower(COALESCE((`+metadata+`)->>'title','')),i.id LIMIT $11 OFFSET $12`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		result = append(result, json.RawMessage(raw))
	}
	return result, total, rows.Err()
}
func (d *DB) LegacyPrintFacets(ctx context.Context, collection string) ([]string, error) {
	rows, err := d.Pool.Query(ctx, `SELECT DISTINCT subject FROM catalog_items i CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(((i.editorial->'legacy_document'->'metadata') || (i.editorial->'legacy_document'->'overrides'))->'subjects')='array' THEN ((i.editorial->'legacy_document'->'metadata') || (i.editorial->'legacy_document'->'overrides'))->'subjects' ELSE '[]'::jsonb END) subject WHERE i.editorial->'legacy_document'->>'published'='true' AND EXISTS(SELECT 1 FROM `+d.catalogView()+` v WHERE v.item_id=i.id AND v.collection_id=$1 AND v.media='print') ORDER BY subject`, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var subject string
		if err = rows.Scan(&subject); err != nil {
			return nil, err
		}
		out = append(out, subject)
	}
	return out, rows.Err()
}

// PrintSeries is a series facet of the print compatibility contract.
type PrintSeries struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// LegacyPrintSeries lists accepted series labels on published print documents.
func (d *DB) LegacyPrintSeries(ctx context.Context, collection string) ([]PrintSeries, error) {
	rows, err := d.Pool.Query(ctx, `SELECT DISTINCT l.id,l.label FROM catalog_labels l JOIN catalog_label_assignments a ON a.label_id=l.id AND a.state='accepted' JOIN catalog_items i ON i.id=a.item_id WHERE l.kind='series' AND i.editorial->'legacy_document'->>'published'='true' AND EXISTS(SELECT 1 FROM `+d.catalogView()+` v WHERE v.item_id=i.id AND v.collection_id=$1 AND v.media='print') ORDER BY l.label,l.id`, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PrintSeries{}
	for rows.Next() {
		var s PrintSeries
		if err = rows.Scan(&s.Slug, &s.Name); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (d *DB) LegacyPrintCollection(ctx context.Context, collection string) (json.RawMessage, error) {
	var raw []byte
	err := d.Pool.QueryRow(ctx, `SELECT jsonb_build_object('id',$1::text,'name',c->'config'->'name','introduction',c->'config'->'introduction','accent',c->'config'->'accent') FROM retained_legacy_exports e CROSS JOIN LATERAL jsonb_array_elements(e.payload->'tables'->'collections') c WHERE e.collection_id=$1 AND e.module='print' AND c->>'id'=$1 ORDER BY e.created_at DESC LIMIT 1`, collection).Scan(&raw)
	return json.RawMessage(raw), err
}
