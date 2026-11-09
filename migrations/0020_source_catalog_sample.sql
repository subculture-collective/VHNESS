-- Supports bounded traversal of the visible recording hash ring. This adds no
-- source/classification/media authority and leaves existing cursor indexes intact.
CREATE INDEX source_catalog_visible_sample_idx
 ON source_catalog_items(collection_id,md5(identifier),identifier)
 WHERE NOT excluded;
