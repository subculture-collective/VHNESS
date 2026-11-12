# Arkhiv


![Arkhiv: three stacked, slightly offset sheets with two staple marks, the top sheet outlined in riso red and blue, beside the name Arkhiv](brand/readme-lockup.png)

Arkhiv is the shared archive engine for independent print, audio, and video apps.
It preserves source metadata, editorial history, reviewed access decisions,
media identities, search indexes, and shared playback schedules.

The branded apps own their frontend source, design, and deployment in separate
repositories: Anarchist Archive (`arkhiv-anarchist`), Riot Grrrl Zine Archive,
Dedhed, Bill Nye TV, and Democracy Now! TV. Arkhiv supplies a versioned headless
engine and optional shared viewer components. Updating the engine does not
replace a brand's interface.

Engine releases use immutable container digests, API and module compatibility
versions, and checksum-locked additive migrations. Each consumer records its
pin and upgrades through a reviewed, independently verified PR. See the
[consumer registry](docs/reference/consumer-registry.json),
[core release contract](docs/reference/core-release-contract.json), and
[retained backend migration guide](docs/retained-backend-migration.md).

The private curation workbench under [`curation/`](curation/README.md) contains
copied Kollektor code, its license, and exact source provenance. It provides
saved searches, bounded discovery, collection review, notes, and exports.
It runs from this repository and connects directly to Arkhiv's catalog;
it does not require a running Kollektor app. Reviewed exports do not grant
acquisition, playback, or public access.

Print compatibility preserves original document and edition IDs, PDF readers,
searchable page text, OCR companions, and covers. Audio compatibility preserves
recording credits, sets, calendar browsing, original audio bytes, and shared
station playback. Explicit public station renewal verifies every selected
recording before extending its timeline.

Democracy Now! (`democracy_now_vid`) remains permanently source-streaming only.
Local preservation for other collections requires explicit review, bounded
storage, verified original bytes, and independent recovery evidence. Original
rights and source credits remain with each item.

The core 0.1.2 source has passed isolated database integration and hosted CI.
Release qualification uses recovered catalogs and the original branded
frontends. Production rollout, owner audiovisual review, and natural operation
are separate evidence. See [development instructions](DEVELOPMENT.md),
[deployment operations](docs/DEPLOYMENT.md), and the [roadmap](ROADMAP.md).

The public SEO frontend has its own
[main CI deployment procedure](docs/runbooks/seo-ci-deployment.md).

The upcoming core 0.4.0 adds a [source metadata catalog](docs/reference/source-metadata-catalog.md)
with cursor discovery, complete snapshot retention, local search and filters, and
corrections that survive re-imports. Metadata ingestion stays independent of media
acquisition and reviewed playback. The Live Music Archive consumer uses this shared
backend; it owns no duplicate catalog schema or importer.

Source metadata can be retained while excluded from browsing with the audited
`set-source-catalog-listing` operator command. See
[retained recording exclusions](docs/reference/source-metadata-catalog.md#excluding-retained-recordings).

Source discovery module 1 adds historical calendar counts across one year, an
inclusive year range or all years; month/day recording search; and cached artist
profiles with retained MusicBrainz/Wikipedia/Commons provenance. See the
[operator and API contracts](docs/reference/source-metadata-catalog.md#artist-and-historical-date-discovery).
The module is additive and requires a normal core release before consumer adoption.

## Brand assets

The product identity and selected public exports are recorded in [the brand handoff](docs/brand/anarchist-archive/GUIDE.md) and [source hashes](docs/brand/anarchist-archive/export.json). Regenerate from SUBCULT Studio with `python3 studio/scripts/export_remaining_brand.py anarchist-archive /path/to/this/repository`; use `--check` to verify the export without writing.

### Anarchist collection identity

![Anarchist Archive identity](docs/brand/anarchist-archive/readme-lockup.png)

This selected collection pack is distinct from Arkhiv’s default identity. See the [collection deployment note](docs/brand/anarchist-archive/DEPLOYMENT.md).
