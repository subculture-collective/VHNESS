# Private print migration inputs

`scripts/prepare_print_migration.py` prepares common catalog inputs from an
independently restored original print pilot. It validates the complete backup
file manifest and database dump, the restore receipt, exported table counts,
original asset sizes/hashes and the complete source metadata. It writes a new
output directory and refuses to overwrite one. It changes no database or archive.

Supply the backup directory containing `complete.json`, `catalog.dump` and
`archive/`, a JSON export of the restored `documents`, `variants`, `assets`,
`collections`, `source_snapshots` and `asset_companions` tables, and a restore
receipt with `restore_exit_code`, `dump_sha256` and `table_counts`.

```sh
python3 scripts/prepare_print_migration.py \
  --backup /private/independent-backup \
  --restored-catalog /private/restored-catalog.json \
  --restore-receipt /private/restore-receipt.json \
  --output /private/new-platform-inputs
```

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
retain original derivatives. Collection limits and deployment scope still
require their retained configuration.

## Applying the plan

`scripts/apply_print_migration.py` applies a prepared directory with the
`vhness` operator commands, in this order:

1. `migrate`.
2. `collection`, with `local_preserve` policy and the `--budget` and
   `--reserve` you pass. Without them, the collection must already exist.
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
python3 scripts/apply_print_migration.py \
  --plan /private/new-platform-inputs --budget 1073741824 --reserve 1073741824
```

Before each step, the script reads the database with `psql` and skips the step
when the database already holds that exact input. Rerunning a completed plan
therefore changes nothing, and rerunning after a failure resumes at the failed
step. A step whose current state differs from the input stops the run instead of
overwriting it, for example a transport review changed after migration or a
collection with other limits. After each command, the script checks that the
database now holds the input.

The first failure stops the run with the step number, command and target. Each
run writes a new receipt (`apply-receipt-<UTC time>.json` in the plan directory,
or `--receipt`) with the plan hash, each step's input file, input hash and
outcome (`applied`, `unchanged`, `ran` for `migrate`, or `failed`), and the
failure message.

The script turns `DATABASE_URL` or `DATABASE_URL_FILE` into libpq variables, so
the connection string never appears on a command line. When the operator
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
