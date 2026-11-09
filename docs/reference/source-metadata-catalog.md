# Source metadata catalog

The shared engine's `source_catalog` module version 1 imports complete source
metadata before any decision to acquire or play media. It is separate from the
reviewed logical-item/representation catalog. A source can remain searchable
without supported files, known dates, reviewed set boundaries or playback approval.

`discover-catalog --collection COLLECTION --pages 1 --items 25 --delay 1s`
uses cursor discovery, persists discovered IDs and the next cursor atomically,
then fetches a bounded metadata queue. All source responses and complete file
inventories are retained through the existing snapshot/observation store. It uses
the engine's metadata-only client, conditional fetches, deadline and Retry-After
handling. It never requests media. Bounds are 100 pages, 1,000 items, a 30-minute
maximum run deadline and a request interval no greater than one minute. Repeated
runs resume progress and persisted backoff. The private operator environment owns
live discovery; the isolated launcher uses fixtures and cannot reach the source.

`refresh-catalog` restarts discovery and requeues known items without deleting
metadata or editorial history. It does not enumerate by date, artist or popularity.
The cursor endpoint avoids the advanced-search 10,000-result limit. An absent
query-specific metadata field partitions source responses: October 13, 2026 live
checks observed response reuse across different queries/cursors with equal fields.

`import-source-catalog --collection COLLECTION --id ID --file metadata.json`
imports an exact saved response and its search projection. This operation requires
matching source identity, selected collection membership and an object-based file
inventory. It retains unknown fields and private entries. Normalization cleans
whitespace, strips HTML from searchable notes, validates calendar dates and may
extract a missing venue from a conventional source title. Derived fields and
uncertainty remain explicit. It infers neither source classification nor sets.

`edit-source-catalog --collection COLLECTION --id ID --file correction.json
--notes 'Reason and evidence'` applies a bounded string-field patch. A null removes
an override and restores the current source value. Overrides and notes have their
own history; a later import replaces only the normalized source projection. The
original response bytes never change. Identity, file inventories and transport
permissions cannot be edited through this command.

Private read-only routes are `/api/source-catalog/v1/items`, `/facets`, `/status`,
`/items/{id}` and `/items/{id}/source`. Every request selects an allowed collection.
The item response includes effective fields, original source metadata, complete
files, normalization provenance, correction history and exact snapshot identity.
Search covers artist, title, venue, place, notes, source credits, lineage, subjects
and track titles. Filters include artist, venue, year, complete date ranges and
availability. Page cursors are bound to the filter and collection. Source-only
inventory is unavailable in public mode; public consumers retain the existing
reviewed catalog and transport API.

`review-source-audio --collection COLLECTION --id ID --notes 'Playback review'`
is a separate explicit operator action for a source-streaming collection. It maps
supported public files with safe filenames into the existing typed audio catalog,
then creates an exact snapshot/file-bound playback review. It grants no acquisition,
download, relay, scheduling or public access. It prefers MP3, M4A, Ogg, FLAC and WAV
and falls back to the audio in an MPEG4 concert recording when available. Codec
decoding remains a browser capability; approvals do not claim every file decodes.
variants and orders filenames naturally. Core 0.4.1 accepts safe nested audio
source paths and escapes each URL segment. Traversal, absolute paths, backslashes
and control characters are rejected. Local preservation retains its existing
root-filename contract.
Playback uses the existing representation resolver and a browser-to-source URL.

Changed source snapshots or recorded withdrawals invalidate those mappings until
an operator re-reviews them. Re-review preserves source and decision history.
Local preservation continues to use the existing manifest, quota, integrity and
independent recovery workflow; the metadata worker never performs acquisition.

`review-source-audio-batch --collection COLLECTION --items 100 --timeout 10m
--notes 'Owner approved private source playback'` explicitly approves a bounded
batch from retained metadata. It never requests media or metadata. Persistent
outcomes distinguish reviewed, unsupported, held and retryable sources. Successful
and unsupported snapshots are skipped on subsequent runs; failures retry after
an hour. Changed metadata requeues the source. Only the batch's unchanged prior
decision can be renewed automatically; independent approvals or revocations need
an operator decision. Single-item review can resolve a held record.

The private status response includes current playback coverage. Counts describe
snapshot-bound approvals, not source CDN availability or browser codec support.
Withdrawn records and changed snapshots cannot count as currently reviewed.

Migration 0014 adds collection-scoped indexes, corrections and cursor worker state;
0015 adds playback-review outcomes and indexes for corpus-scale review and reporting.
Existing source snapshots, catalog decisions, media approvals and schedules are
retained. Old consumers use their existing APIs. Core 0.4.0 introduced this module;
core 0.4.1 adds batch playback review and nested source audio. Package updates and public
rollout remain separate release decisions.

## Excluding retained recordings

An operator can remove records from source-catalog browse, search, facets and
HTTP detail/source routes while retaining their exact snapshots, corrections,
review attempts and import accounting. Listing decisions survive metadata
re-imports and remain separate from independent transport permissions.

Use the operator command:

```sh
vhness set-source-catalog-listing --collection etree --file listing.json \
  --notes "Owner excluded unsupported recordings"
```

 The JSON input is
`{"identifiers":["SOURCE_ID"],"listed":false}`. A decision accepts 1–1000 unique
existing identifiers, applies atomically, and records each changed listing in
source history. Set `listed` to `true` with new notes to restore them.

Status retains the full `imported` count and reports `excluded` separately.
Search totals, facets and playback coverage omit excluded records. The playback
review worker skips them. Listing is a presentation decision; revoke independent
transport reviews separately when removing previously granted media permissions.

Private source-catalog list, facet and status aggregation have a ten-second database
budget for full collections. Other API routes, including readiness and playback,
keep their three-second budget. Playback totals project narrow review and
membership fields before joining, retaining current snapshot and listing checks.

## Artist and historical date discovery

`source_discovery` module 1 adds read-only collection-scoped endpoints:

- `GET /api/source-catalog/v1/calendar?collection=etree&month=05` returns dated
  recording counts across all years. Supply both `year_from` and `year_to` for an
  inclusive range; equal bounds select a single year. Optional `artist` uses the
  catalog's exact, case-insensitive creator filter.
- Recording search accepts `month`, `month_day` (`MM-DD`), `year_from`, `year_to`
  and `sort=date_desc`. Date order uses date plus identifier keyset cursors,
  rejects changed filters, and includes only complete valid dates. February 29
  is valid for anniversary selection; invalid source dates remain retained.
- `GET /api/source-catalog/v1/artist?collection=etree&artist=Ween` returns visible
  recording counts and date bounds, plus a cached enrichment profile when bound.

`source_artist_photos` module 1 adds one read-only, collection-scoped list:

- `GET /api/source-catalog/v1/artist-photos?collection=etree` returns
  `{"artists":[{"artist":"ween","photo":{...}}]}` for every bound artist profile
  with an attributed photograph. `artist` is the lowercased source artist key;
  match it case-insensitively against recording creators. `photo` has the same
  URL, Commons source, credit and license fields as the artist profile. Artists
  whose recordings are all excluded are omitted. Directory and recording views
  can show photographs without one artist request per row.

`source_artist_media` module 1 stores artist photographs in the engine and
allows a second identity:

- `GET /api/source-catalog/v1/artist-media/{sha256}` returns a stored rendition.
  A photo's `image` is at most 1280 pixels wide and its `thumbnail` 330 pixels;
  each has `sha256`, `content_type`, `width` and `height`. Both are re-encoded
  as JPEG, or PNG when the image has transparency, so embedded metadata such as
  camera location is dropped. Responses are immutable, `nosniff` and sandboxed.
  A rendition is served only while a current profile of an artist with visible
  recordings references it.
- A profile may be bound to the Internet Archive collection that holds the
  artist's recordings (`archive_collection`, `archive_url`), with or without a
  MusicBrainz ID. Import retains the collection's metadata response and checks
  that it is a collection inside the catalog's collection and that at least one
  of the artist's visible recordings belongs to it. The profile name is then the
  collection title, and `musicbrainz_id` is null when absent.
- A collection-bound profile can use the collection page's text as its
  biography (license `Internet Archive collection description`) and its item
  image as the photograph (license `Internet Archive collection image`, no
  license URL). Neither has a separate license. Consumers must label them as
  the collection's own material, not as Wikipedia or Commons content.

All discovery queries omit excluded recordings. Counts are recordings, not
independently deduplicated concerts. Artist enrichment is independent of source
recording metadata, corrections, import queues and playback decisions. Unknown
or excluded-only artists return 404. Public-only engine mode keeps the private
source inventory unavailable, as for the other source-catalog routes.

Artist enrichment is an explicit operator workflow. Verify the MusicBrainz
identity first, then run the metadata-only helper outside an isolated test
network, writing a private operator bundle:

```sh
python3 scripts/enrich_source_artist.py --mbid c0eee88b-47f2-4cd2-ac48-a045e902a432 \
  --output /private/operator/ween-artist.json
vhness import-source-artist --collection etree --artist Ween \
  --file /private/operator/ween-artist.json --notes "Verified the Ween artist identity and linked sources"
```

The helper never searches by artist name. It follows the reviewed MusicBrainz
entity's Wikidata relationship to Wikipedia and Commons, preserving exact raw
JSON responses. It caches for seven days and serializes outbound calls at 1.1
seconds with an identifying User-Agent; `--refresh` bypasses the response cache.
Provider throttling persists a cooldown. Import requires matching MusicBrainz
response identity, bounded sources, attribution and operator notes, and appends
profile history. The helper never requests audio. It downloads the selected
photograph (a 1280-pixel Commons rendering, or the collection's item image) into
the bundle as `data_base64` with its `data_url`; import stores renditions, not
the downloaded bytes. Uncredited images are omitted. Biography text is attributed to Wikipedia
contributors under CC BY-SA 4.0; Commons images retain their individual licenses.
The photograph is the Wikidata image (P18). Without one, the article's lead image
is used only when it is a Commons file; locally hosted, often non-free, images
are skipped. Public-domain files have no license URL and GFDL or Free Art License
files link other sites, so those photographs link the Commons file page's
licensing section, which the import accepts.

`--archive-collection ID` adds the Live Music Archive collection identity, alone
or with `--mbid`. Determine the collection from the artist's own recordings'
retained `metadata.collection` values, not from a name search. Wikipedia and
Commons take precedence. The collection page text is used only when generated
download notices and paragraphs about taping, trading or archive permission
leave at least 25 words; the image is the file whose format is
`Item Image`. Pass `--no-archive-image` for a placeholder shared by several
collections, and `--no-archive-description` to omit the text.

A new core release/adoption is required before consumers rely on this module.
Do not edit an existing published receipt to advertise unreleased capability.
The contract source includes the additive capability for the next release.

## Recording order

Private item queries accept `sort=date_desc`, `sort=date_asc`, and
`sort=artist_asc`; omitted order retains identifier order. Date orders include
only complete valid dates and break equal-date ties by identifier ascending.
Artist order uses the database lowercased creator key ascending, then identifier.
Missing artist keys sort first. Artist order retains incomplete/unknown dates.
All orders apply to the full filtered set before the 100-record page boundary.
Cursors bind the collection, filters, and order; changed queries are rejected.
The cursor carries the exact database artist key, not a consumer-derived key.
This source change requires a published core release and explicit consumer
adoption before consumers rely on the added orders.

`TestRecordingSortPlans` explains the exact search statement on 5,001 disposable
synthetic projections. Date orders used the existing visible-date bitmap index
and a top-N sort; artist order used a sequential scan and top-N sort. Observed
execution was 4.834 ms (newest), 4.246 ms (oldest), and 6.898 ms (artist), with
533/533/501 shared buffer hits. Full matching rows are still materialized for the
exact total, and `Search` still computes catalog status separately. These plans
prove ordering placement and expose costs; they do not qualify production latency.
The separately scoped loading work must measure representative corpus sizes and
status/coverage costs before claiming the 2.5-second first-row budget.
