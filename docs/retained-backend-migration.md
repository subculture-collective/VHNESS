# Retained print and audio backend migration

The core can serve retained branded interfaces with `ARKHIV_COMPATIBILITY=print`
or `audio`. Compatibility serving requires exactly one configured collection.
The native combined catalog remains available. Each brand keeps its own database,
media roots, schedules, credentials and operator decisions.

Print routes retain `/api/collections`, `/api/documents`, `/api/facets`, and
`/api/assets/{variant}` with cover, search and download actions. Document and variant
identifiers remain stable. Effective metadata merges the retained source and editorial
overrides. Public responses expose the same metadata fields as the original print
API. Rights text remains separate from access. Full-text browsing uses retained native
page indexes; range reads and downloads verify the current original against its
reviewed preservation manifest before serving bytes. `/api/documents?series=` and the `series` list in
`/api/facets` add accepted series labels, and the `creators` list in `/api/facets`
adds credited people with document counts, to this contract without changing the
existing fields ([catalog labels](reference/catalog-labels.md#series)).

Audio routes retain `/api/recordings`, `/api/audio/{recording}`, `/api/station`,
`/api/calendar`, `/api/dates/{date}`, `/api/on-this-day` and `/api/shows`. The adapter
uses current core transport decisions and verified continuous audio receipts. Ordered
tracks, reviewed sets, source/taper/transfer credits, original recording identifiers,
legacy source hashes and the station revision/start/duration/offset/next contract remain
available. Withdrawal of a future scheduled recording makes the station unavailable;
on-demand eligibility is independently checked. Conditional and range requests check
current rights and integrity before responding. Error responses on the recording,
station, calendar and date routes keep Dedhed's status codes and wording, because its
frontend shows them to listeners; a station that cannot play its current recording
reports that it is off the air.

The migration preparation scripts validate an independently recovered backup before
writing a new output directory. They keep full donor table exports and source identity
mappings. `retain-legacy` stores those exports immutably and projects the complete audio
inventory for calendar/search views. Public inventory visibility follows current core
approval; private recovery history remains outside public responses.

Print preparation also writes `index-{variant}.json` and an `indexes` plan list
for already published editions with successful retained derivatives. After importing
the retained export and its current transport review, run `restore-print-index
--id DOCUMENT --representation VARIANT --collection COLLECTION --file INPUT`.
Set `VHNESS_MEDIA` and `VHNESS_DERIVATIVES` to the independently recovered archive
root. The command compares every page and the original extraction metadata with the
immutable donor export, verifies PDF and cover hashes, and checks current reading and
OCR decisions. Whole-document OCR remains page zero. It preserves existing derivatives
without re-running extraction or granting additional access. Unpublished editions and
failed derivatives stay in recovery history for later explicit review.

For an adopted public audio station, `renew-public-audio --collection COLLECTION
--file SELECTION --hours 168 --notes BASIS` replaces the legacy renewal command.
`SELECTION` is a JSON array of exact `item_id` and `representation_id` pairs.
The command verifies all preserved inputs and continuous media, rejects private or
withdrawn recordings, and repeats only this explicit selection. Repeating it within
the same hour preserves the same timeline and revision. It does not add recordings,
acquire sources, or change access reviews. Keep the brand's maintenance lock and
operator schedule when switching its renewal command.

By default, migration preparation grants no public approval. With
`--retain-public-approvals`, print preparation retains only already published and
approved variants. Audio preparation requires the donor's current approved audience
recording, exact source and audio hashes, reviewer and policy basis. It preserves the
existing on-demand/shared-radio approval without adding acquisition or download rights.
The resulting public reviews still require the core's explicit `review-transport
--approve-public` operator path. These are retained owner decisions, not new permission.

Qualification before cutover:

1. Capture fresh complete donor dumps, media manifests, source responses and table exports.
2. Verify an independent recovery copy and restore into isolated candidate databases.
3. Import source snapshots, catalog mappings and retained exports, then register existing
   originals/derivatives with matching integrity/recovery receipts. Import current access
   decisions and the unchanged station timeline; never acquire additional media.
4. Preserve print page indexes/covers and audio maintenance behavior. Compare legacy and
   native catalog/reader/player contracts, full asset hashes, byte ranges, reviewed set
   boundaries, schedule timing and scope/withdrawal behavior.
5. Qualify the existing branded frontend images in a browser, publish a new immutable core
   release, then pin it in each consumer. Retain original images, databases, media and a
   scoped rollback receipt through qualification.

Implementation checks do not prove that a retained production backend has been migrated.
Fresh full restores, browser qualification, pinned release delivery, maintenance migration
and live cutover evidence remain required before declaring conversion complete.
