-- A collection-level automatic public access decision is a recorded operator
-- decision. This additive migration records none and approves no content. The
-- review-public-collection command applies a decision to print representations
-- that already hold a current private reading review and links every grant or
-- revocation it writes to the decision, so transport history shows it as
-- automatic. A NULL link marks an ordinary operator review.
CREATE TABLE collection_public_access (
 collection_id text PRIMARY KEY REFERENCES collections(id),
 automatic boolean NOT NULL,
 notes text NOT NULL CHECK(length(trim(notes)) > 0),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE collection_public_access_history (
 id bigserial PRIMARY KEY,
 collection_id text NOT NULL REFERENCES collections(id),
 automatic boolean NOT NULL,
 notes text NOT NULL CHECK(length(trim(notes)) > 0),
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE representation_transport_reviews
 ADD COLUMN automatic_access bigint REFERENCES collection_public_access_history(id);
ALTER TABLE representation_transport_history
 ADD COLUMN automatic_access bigint REFERENCES collection_public_access_history(id);
