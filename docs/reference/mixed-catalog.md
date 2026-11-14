# Mixed catalog API

Arkhiv uses a catalog item for a reviewed editorial identity and a representation
for one typed print, audio or video edition. A source item/file is provenance;
it does not force editorial grouping. Several documents can map to separate items
from the same source, while one talk can have print and audio representations.
Recording IDs, concert groups, ordered tracks, reviewed sets and credits remain
distinct from print variants/pages and video episodes/captions.

`GET /api/catalog/v1/items` returns `items`, `total`, `page` and
`search_coverage`. Optional `collection`, `media` (`print`, `audio`, `video`),
`q` and `page` filter the result. Pages contain at most 24 items. Search coverage
is `metadata` unless `text` is set (core 0.4.6). `text` searches the indexed page
text of the listed representations, including page-mapped OCR companions; it
reports `["metadata","text"]` and each item carries up to three `text_hits`
(`representation`, `page`, `snippet`). Only text indexed for the representation's
current snapshot matches, so withdrawn or stale editions do not leak through
search. `label` limits results to items with that accepted label; items list
their accepted `labels`. A `series` label returns its items in issue order, and
its entry in `labels` carries `issue_position` and `issue_label`
([catalog labels](catalog-labels.md#series)). Caption and transcript search are not advertised.
`GET /api/catalog/v1/creators?collection=` (core 0.4.11) lists the people credited
on items visible in the audience scope as `[{name, items}]`, where `items` is the
number of items that credit them. Credits are split on commas and trimmed, and
names that differ only in case are one person, shown with the spelling of the
newest item that uses it. The list is sorted by name, ignoring case. A public
server counts only public items; a foreign collection returns 404.
`GET /api/catalog/v1/items/{id}` uses the same collection and representation
visibility checks. A foreign collection returns 404 under the existing server
collection scope. An item exposes only representations and membership visible
within that scope and the selected media filter.

The additive migration retains all original video tables and history. The
`legacy-video:{episode-id}` catalog projection reads current source fields and
reviewed decisions directly. Withdrawal immediately removes the catalog entry;
no copied decision can remain stale. Each projected representation includes the
original `legacy_episode_id` for the existing video API. Existing `/api/episodes`,
`/api/playback`, `/api/channel`, media URLs and schedules continue unchanged.

New catalog mappings preserve complete source snapshots and independent editorial
metadata. Insertion validates source ownership, actual unrestricted file entries,
typed extensions and exact item/representation collection membership in one
transaction. A failed mapping leaves no partial catalog item. Duplicate source
identity is never a reason to overwrite another reviewed editorial item. Mapping
notes and the complete reviewed payload are retained in catalog history.

Catalog listing grants no playback, reading, downloads, relay, acquisition or
public rights. Typed transport and policy adapters are the next implementation
unit. No print/audio media is served by this initial catalog API. Its schema is
additive, and recovery fingerprints now include all five new catalog tables.
Rollback to the prior video engine can retain these tables; changing or removing
their history requires a separately qualified migration.
