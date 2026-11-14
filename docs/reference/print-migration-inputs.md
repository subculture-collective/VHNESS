# Private print migration inputs

`scripts/prepare_print_migration.py` prepares common catalog inputs from an
independently restored original print pilot. It validates the complete backup
file manifest and database dump, the restore receipt, exported table counts,
original asset sizes/hashes and the complete source metadata. It writes a new
output directory and refuses to overwrite one. It changes no database or archive.

Supply the backup directory containing `catalog.dump` and `archive/`, its
manifest `complete.json`, a JSON export of the restored legacy tables, and a
restore receipt with `restore_exit_code`, `dump_sha256` and `table_counts`.
`scripts/export_print_legacy.py` writes the last three.

## Exporting the legacy inputs

Restore `catalog.dump` into an empty, isolated database, then run the export
against it. `--backup` is the same directory prepare reads: `catalog.dump` must be
the dump you restored, and `archive/` the archive root (it may be a link to it).

```sh
DATABASE_URL_FILE=/private/restored-legacy.url \
python3 scripts/export_print_legacy.py \
  --backup /private/independent-backup --source backup \
  --restore-exit-code 0 --output /private/legacy-export
```

The export reads all seventeen legacy tables in one read-only statement and stops
if the database has a table it does not know, so nothing is left out. It hashes
the dump and every file under `archive/`, and stops at a link or special file.
It writes a new directory (mode 0700) with three files, each mode 0600:

- `complete.json`: the backup manifest, with the dump hash and size, every
  archive file's SHA-256, and `source`.
- `restored-catalog.json`: every row of every table. `page_text` omits its
  generated `search` column.
- `restore-receipt.json`: the restore exit code, dump hash, row count per table,
  `source` and the hash of `restored-catalog.json`.

It changes no database, dump or archive. Exporting the same restored database
twice writes the same table export and receipt. As in apply, `DATABASE_URL` or
`DATABASE_URL_FILE` becomes libpq variables, so the connection string never
appears on a command line; `--psql` takes a prefix such as `docker exec -i db
psql -U arkhiv -d restored`.

`--source` is required and is recorded in the manifest and receipt:

- `backup`: the archive and dump are an independent off-host backup copy. Only
  this is evidence for a cutover.
- `live`: the live archive and a fresh dump of the live database. Use it only
  for rehearsals.

The export cannot check that the restored database came from `catalog.dump`, or
that a `backup` source really is off-host; the operator states both.

`scripts/test_export_print_legacy.py` exports a synthetic fixture through a fake
database, then prepares and applies it. With `ARKHIV_TEST_LEGACY_DATABASE_URL`
and `ARKHIV_TEST_DATABASE_URL` (two disposable databases) and `VHNESS_BIN` set,
it builds the legacy schema in the first, exports it with `psql`, and applies the
plan twice to the second through the real commands.

## Preparing the plan

```sh
python3 scripts/prepare_print_migration.py \
  --backup /private/independent-backup \
  --manifest /private/legacy-export/complete.json \
  --restored-catalog /private/legacy-export/restored-catalog.json \
  --restore-receipt /private/legacy-export/restore-receipt.json \
  --output /private/new-platform-inputs
```

`--manifest` defaults to `BACKUP/complete.json`. Prepare refuses a manifest whose
`source` is `live`, or that records no source, unless you pass
`--allow-live-source`; then it warns, and the plan records `evidence_source` and
`cutover_evidence: false`. A plan from a `backup` source records
`cutover_evidence: true`. Prepare also refuses a restore receipt whose source or
table export hash differs from the files it is given.

Documents and variants retain their IDs and grouping. Complete document and
variant rows remain in editorial provenance; original metadata and reviewed
overrides remain distinct. OCR companions stay attached to their editions.
Unpublished listings receive no transport inputs. Retained private published
documents receive private reading/download inputs; acquisition, playback, relay,
scheduling and public access remain disabled.

Legacy snapshot IDs can refer to a different JSON serialization. The converter
checks complete semantic equality between the restored database and the retained
source file, preserves the old identity, and binds the common source to the exact
file bytes. `migration-plan.json` records both identities. It never substitutes
new network metadata or invents missing original response bytes.

Keep the original database dump and fresh legacy restore as the authority for
all prior decisions, acquisition events, text indexes and schema history. Common
catalog import adds its own migration/transport history; it does not reinterpret
legacy events as newly granted rights. Rebuild common derivatives separately and
retain original derivatives. `migration-plan.json` records each collection's
legacy limits in `collection_limits`: `budget` from `limits.total_bytes`, and
`reserve` from `updates.minimum_free_bytes`, or the legacy app's 2 GiB free-space
floor when that is unset. Prepare refuses a collection without a positive
`limits.total_bytes`. Deployment scope still requires its retained configuration.

## Applying the plan

`scripts/apply_print_migration.py` applies a prepared directory with the
`vhness` operator commands, in this order:

1. `migrate`.
2. `collection`, with `local_preserve` policy. Each collection gets the
   budget and reserve the plan records for it in `collection_limits`. Passing
   `--budget` and `--reserve` overrides them, with the same values for every
   collection in the plan. A plan prepared before `collection_limits` existed
   needs them, or the collection must already exist. The receipt records each
   collection's values and whether they came from the plan or the override.
3. `retain-legacy` for each `retained-export-*.json`. This stores the donor's
   collection configuration, so the compatibility `/api/collections` returns
   the collection name, introduction and accent.
4. `import-source` for each `source-*.json`, after checking its bytes against
   the plan.
5. `import-catalog` for each `item-*.json`, keeping legacy document and
   variant IDs.
6. `review-transport` for each `review-*.json`, with `--approve-public` only
   when the prepared review is public.
7. `restore-print-index` for each `index-*.json`.

```sh
DATABASE_URL=... VHNESS_COLLECTION=zines \
VHNESS_MEDIA=/private/independent-backup/archive \
VHNESS_DERIVATIVES=/private/independent-backup/archive \
python3 scripts/apply_print_migration.py --plan /private/new-platform-inputs
```

Before each step, the script reads the database with `psql` and skips the step
when the database already holds that exact input. Rerunning a completed plan
therefore changes nothing, and rerunning after a failure resumes at the failed
step. A step whose current state differs from the input stops the run instead of
overwriting it, for example a catalog item or transport review changed after
migration, or a collection with other limits. `review-budget` can only raise a
collection's limits, so a collection created with a larger budget than the plan's
keeps it; pass the same override again to rerun. Catalog items compare their
fields, editorial record, listed memberships and representations, not only
their IDs. After each command, the script checks that the
database now holds the input.

The first failure stops the run with the step number, command and target. Each
run writes a new receipt (`apply-receipt-<UTC time>.json` in the plan directory,
or `--receipt`) with the plan hash, each step's input file, input hash and
outcome (`applied`, `unchanged`, `ran` for `migrate`, or `failed`), and the
failure message.

The script turns `DATABASE_URL` or `DATABASE_URL_FILE` into libpq variables, so
the connection string never appears on a command line. It accepts a
`postgres://` URL or a keyword string (`host=... dbname=...`). Settings the
string omits keep their inherited `PG*` values, as they do for `vhness`. It stops before
any command when a setting has no libpq variable, such as a multi-host URL or a
`pool_*` parameter. When the operator
commands run in a container, pass a prefix such as `--vhness "docker compose
exec -T api vhness"` and an equivalent `--psql`; input paths must then resolve
the same inside the container. `review-transport --approve-public` is still the
only path to public access, and only for reviews prepared with
`--retain-public-approvals`.

`scripts/test_apply_print_migration.py` covers ordering, reruns and failures
with a fake runner. With `ARKHIV_TEST_DATABASE_URL` (an empty, disposable
database) and `VHNESS_BIN` set, it also applies a synthetic fixture collection
twice through the real commands, checks that the second run leaves every table
unchanged, and reads the collection and documents back from the compatibility
API.

For a collection whose legacy documents were public through an automatic
collection policy, record the owner's decision after indexing with
`review-public-collection --approve-public` (see
[public core](public-core.md#automatic-collection-access)). It publishes the
retained private reading reviews that qualify. Approvals kept with
`--retain-public-approvals` stay manual. A variant whose legacy public
approval was revoked while its document stayed published also receives a private
reading input, so record its private review again after the automatic decision.
That review is the core's explicit withdrawal.

Generated inputs are a preparation artifact. Qualify actual catalog coverage,
PDF reading/search, original-download hashes, an independent common database
restore and a rollback to the retained legacy app before any cutover decision.
