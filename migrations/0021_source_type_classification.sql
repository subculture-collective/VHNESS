-- Recording source type, derived from explicit evidence in the effective source
-- and lineage fields. Retained snapshots are untouched. Rows written before this
-- migration keep version 0 and read as unknown until the bounded backfill runs.
ALTER TABLE source_catalog_items
 ADD COLUMN source_type text NOT NULL DEFAULT 'unknown'
  CHECK(source_type IN ('soundboard','audience','matrix','fm_radio','other','unknown')),
 ADD COLUMN source_type_version integer NOT NULL DEFAULT 0,
 ADD COLUMN source_type_evidence jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE FUNCTION source_catalog_source_labels(value text) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
 text_value text := lower(COALESCE(value,''));
 labels text[] := '{}';
 named text := 'sbd|soundboard|sound[ -]board|mixing[ -]board|board[ -](tape|recording|mix|feed|patch)|aud|audience|matrix|mtx|fm|radio|broadcast';
 hedged boolean;
BEGIN
 -- Phrases that use a label word without describing the recording's source.
 text_value := regexp_replace(text_value, '\maudience[[:space:]]+(noise|chatter|talk|talking|clap|claps|clapping|applause|participation|members?|sing|singing)\M', ' ', 'g');
 text_value := regexp_replace(text_value, '\mpre[ -]?fm\M', ' ', 'g');
 hedged := text_value ~ ('\m(not|no|non|without|maybe|possibly|probably|perhaps|likely|allegedly|supposedly|unsure|uncertain)[[:space:]-]+(an?[[:space:]]+)?(' || named || ')\M')
  OR text_value ~ ('\m(' || named || ')\M[[:space:]]*\?');
 IF text_value ~ '\m(studio[ -](recording|session|master)|webcast|live[ -]?stream|(tv|television|cable|satellite)[ -]broadcast|camcorder|video[ -](source|recording|soundtrack|audio))\M' THEN
  labels := labels || 'other'::text;
  text_value := regexp_replace(text_value, '\m(tv|television|cable|satellite)[ -]broadcast\M', ' ', 'g');
 END IF;
 IF text_value ~ '\m(sbd|soundboard|sound[ -]board|mixing[ -]board|board[ -](tape|recording|mix|feed|patch))\M' THEN labels := labels || 'soundboard'::text; END IF;
 IF text_value ~ '\m(aud|audience)\M' THEN labels := labels || 'audience'::text; END IF;
 IF text_value ~ '\m(matrix|mtx)\M' THEN labels := labels || 'matrix'::text; END IF;
 IF text_value ~ '\m(fm|radio|broadcast)\M' THEN labels := labels || 'fm_radio'::text; END IF;
 RETURN jsonb_build_object('labels', to_jsonb(labels), 'hedged', hedged);
END $$;

-- An editor's reviewed classification wins. Otherwise a single explicit label
-- decides; a matrix may also name its soundboard and audience parts. Missing,
-- conflicting or hedged evidence stays unknown.
CREATE FUNCTION source_catalog_classify(effective jsonb) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
 version constant integer := 1;
 reviewed text := effective->>'source_type';
 from_source jsonb := source_catalog_source_labels(effective->>'source');
 from_lineage jsonb := source_catalog_source_labels(effective->>'lineage');
 labels text[];
 fields text[] := '{}';
 result text := 'unknown';
 reason text;
BEGIN
 IF reviewed IN ('soundboard','audience','matrix','fm_radio','other','unknown') THEN
  RETURN jsonb_build_object('type', reviewed, 'version', version, 'evidence', jsonb_build_object('reason','editorial','fields','["source_type"]'::jsonb,'labels','[]'::jsonb));
 END IF;
 SELECT COALESCE(array_agg(DISTINCT label ORDER BY label), '{}') INTO labels
  FROM (SELECT jsonb_array_elements_text(from_source->'labels') AS label
        UNION ALL SELECT jsonb_array_elements_text(from_lineage->'labels')) found;
 IF jsonb_array_length(from_source->'labels') > 0 THEN fields := fields || 'source'::text; END IF;
 IF jsonb_array_length(from_lineage->'labels') > 0 THEN fields := fields || 'lineage'::text; END IF;
 IF (from_source->>'hedged')::boolean OR (from_lineage->>'hedged')::boolean THEN reason := 'ambiguous';
 ELSIF cardinality(labels) = 0 THEN reason := 'no_evidence';
 ELSIF 'matrix' = ANY(labels) AND labels <@ ARRAY['matrix','soundboard','audience'] THEN result := 'matrix'; reason := 'matched';
 ELSIF cardinality(labels) = 1 THEN result := labels[1]; reason := 'matched';
 ELSE reason := 'conflicting';
 END IF;
 RETURN jsonb_build_object('type', result, 'version', version, 'evidence', jsonb_build_object('reason',reason,'fields',to_jsonb(fields),'labels',to_jsonb(labels)));
END $$;

CREATE OR REPLACE FUNCTION project_source_catalog() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 classification jsonb;
BEGIN
 NEW.effective := NEW.normalized || NEW.editorial;
 NEW.search := to_tsvector('simple',
  COALESCE(NEW.effective->>'title','') || ' ' || COALESCE(NEW.effective->>'creator','') || ' ' ||
  COALESCE(NEW.effective->>'venue','') || ' ' || COALESCE(NEW.effective->>'coverage','') || ' ' ||
  COALESCE(NEW.effective->>'date','') || ' ' || COALESCE(NEW.effective->>'description','') || ' ' ||
  COALESCE(NEW.effective->>'taper','') || ' ' || COALESCE(NEW.effective->>'transferer','') || ' ' ||
  COALESCE(NEW.effective->>'source','') || ' ' || COALESCE(NEW.effective->>'lineage','') || ' ' ||
  COALESCE(NEW.effective->>'subject','') || ' ' || COALESCE(NEW.effective->>'songs',''));
 classification := source_catalog_classify(NEW.effective);
 NEW.source_type := classification->>'type';
 NEW.source_type_version := (classification->>'version')::integer;
 NEW.source_type_evidence := classification->'evidence';
 RETURN NEW;
END $$;

CREATE INDEX source_catalog_visible_source_type ON source_catalog_items
 (collection_id,source_type,(effective->>'date'),identifier) WHERE NOT excluded;
CREATE INDEX source_catalog_source_type_pending ON source_catalog_items
 (collection_id,identifier) WHERE source_type_version = 0;
