-- The legacy-video branch of the catalog view read five fields from each
-- episode's full source document separately. That document carries the whole
-- source file inventory, so each field unpacked it again, and listing the
-- public catalog took about 1.5 seconds. Read the fields in one pass.
CREATE OR REPLACE VIEW visible_catalog_representations AS
 SELECT i.id AS item_id,i.title,i.description,i.date,i.creator,m.collection_id,
 r.id AS representation_id,r.media,r.source_item_id,r.source_hash,r.file,r.extension,
 ''::text AS legacy_episode_id
 FROM catalog_items i
 JOIN catalog_memberships m ON m.item_id=i.id AND m.listed
 JOIN catalog_representation_memberships rm ON rm.item_id=i.id AND rm.collection_id=m.collection_id AND rm.listed
 JOIN catalog_representations r ON r.item_id=i.id AND r.id=rm.representation_id
 UNION ALL
 SELECT 'legacy-video:'||e.id,
 COALESCE(NULLIF(e.decision->>'title',''),s.title),
 COALESCE(s.description,''),COALESCE(s.date,''),COALESCE(s.creator,''),
 e.collection_id,'video','video',COALESCE(NULLIF(s.source_item_id,''),e.id),
 e.source_hash,COALESCE(e.decision->>'file',''),
 jsonb_build_object('video',jsonb_build_object('episode_id',e.id,'captions',COALESCE(e.decision->'captions','[]'::jsonb))),e.id
 FROM episodes e
 CROSS JOIN LATERAL jsonb_to_record(e.source) AS s(title text,description text,date text,creator text,source_item_id text)
 WHERE e.decision @> '{"published":true}'::jsonb OR e.decision->'processed_video' @> '{"published":true}'::jsonb;
