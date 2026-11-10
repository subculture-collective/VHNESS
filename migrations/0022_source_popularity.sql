-- Dated provider responses and the last usable view counts per recording. Kept
-- apart from recording metadata and editorial history; page reads only read them.
CREATE TABLE source_popularity_responses (
 id bigserial PRIMARY KEY,
 provider text NOT NULL,
 sha256 text NOT NULL,
 observed_at timestamptz NOT NULL,
 requested jsonb NOT NULL,
 raw bytea NOT NULL CHECK(octet_length(raw) <= 1048576),
 valid boolean NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(provider,sha256,observed_at)
);
CREATE TABLE source_popularity_snapshots (
 collection_id text NOT NULL,
 identifier text NOT NULL,
 provider text NOT NULL,
 have_data boolean NOT NULL,
 all_time bigint CHECK(all_time >= 0),
 last_30day bigint CHECK(last_30day >= 0),
 last_7day bigint CHECK(last_7day >= 0),
 observed_at timestamptz NOT NULL,
 response_id bigint NOT NULL REFERENCES source_popularity_responses(id),
 last_attempted_at timestamptz NOT NULL,
 last_outcome text NOT NULL CHECK(last_outcome IN ('usable','no_data','malformed','omitted')),
 PRIMARY KEY(collection_id,identifier,provider),
 FOREIGN KEY(collection_id,identifier) REFERENCES source_catalog_items(collection_id,identifier),
 CHECK(have_data = (all_time IS NOT NULL AND last_30day IS NOT NULL AND last_7day IS NOT NULL))
);
