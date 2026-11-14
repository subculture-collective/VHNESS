-- Ordered series. A `series` label groups the issues of a periodical; each
-- assignment to a series label carries an integer issue position (the reading
-- order) and the issue's own label, such as "Volume 3" or "42 (print)". Other
-- kinds carry neither. Assignments still pass through the reviewed proposal
-- workflow; this migration accepts nothing. The migrate reader grant covers
-- the new columns because it grants SELECT on whole tables.
ALTER TABLE catalog_labels DROP CONSTRAINT catalog_labels_kind_check,
 ADD CONSTRAINT catalog_labels_kind_check CHECK(kind IN ('topic','place','organization','person','event','series'));
ALTER TABLE catalog_label_assignments
 ADD COLUMN issue_position integer CHECK(issue_position >= 0),
 ADD COLUMN issue_label text CHECK(length(trim(issue_label)) BETWEEN 1 AND 200),
 ADD CONSTRAINT catalog_label_assignments_issue CHECK((issue_position IS NULL) = (issue_label IS NULL));
ALTER TABLE catalog_label_history
 ADD COLUMN issue_position integer,
 ADD COLUMN issue_label text;
