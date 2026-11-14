# Development and verification

Use profile `vhness` from the current worktree after reading the shared
`~/.local/share/t3-dev-environments/AGENT-GUIDE.md`.

```sh
t3-dev --profile vhness start
t3-dev --profile vhness sync
t3-dev --profile vhness verify
t3-dev --profile vhness test --command 'make test-integration'
t3-dev --profile vhness stop
```

The isolated preview seeds three explicitly synthetic metadata records and stores
no video. Application/test networks cannot reach Internet Archive. The preview
database is retained; tests use a separate disposable PostgreSQL database and
temporary schemas. Do not point `TEST_DATABASE_URL` at preview or production.

`make verify` checks Go formatting, vet, race tests, binary build, frozen web
dependencies, TypeScript, lint, web tests, and production build. Frontend tests cover channel clock offsets, asset identity, and permitted playback URLs;
browser proof is a separate gate. Database
tests report a skip if `TEST_DATABASE_URL` is absent. Explicit integration refuses
an absent URL. Actual playback, live source discovery, source failure, independent
restore, hosted CI, and complete pilot qualification require separate receipts.

The CLI requires `DATABASE_URL`. Run it under `backend/`; migration paths default
to `../migrations`. `VHNESS_MIGRATIONS` can select the explicit migration directory.
`ARKHIV_READER_ROLE` names an existing read-only role that `migrate` grants SELECT
on every table in the migration schema.

```sh
go run ./cmd/vhness migrate
go run ./cmd/vhness collection --collection democracy_now_vid --policy source_stream
go run ./cmd/vhness discover --collection democracy_now_vid --pages 1 --items 20 --timeout 10m
go run ./cmd/vhness report
```

Live discovery runs only in a separately configured private operator environment,
outside the isolated preview. Bounds limit one invocation; repeat invocations
resume persisted progress and backoff. `refresh` resets discovery for reconciliation
and queues a bounded known-item batch, retaining source and decision history.
`import`, `review`, and `schedule` accept exact JSON input files. Operator mutations
are CLI-only; web APIs are read-only. `API_ADDR` defaults to loopback port 8080.

Raw source responses, observations, every file inventory, decisions, and schedules
remain in PostgreSQL. Metadata workers cannot request download endpoints, and
streaming playback resolves a browser-to-source URL. The source-streaming adapter has no media proxy. A separate local adapter serves verified originals for explicitly local collections.


## Private pilot and preservation fixtures

`scripts/pilot.py start|status|stop` manages the owned loopback API and retained
PostgreSQL 17 volume. Build requires this worktree's verified frontend snapshot.
`scripts/pilot.py run <CLI arguments>` reads the private environment without
printing its credentials. The API defaults to `127.0.0.1`; no public ingress is
configured. Before replacing its binary, stop only this pilot API.

`python3 scripts/generated_fixture.py` creates two explicitly synthetic
12-second H.264/AAC originals, each under 5 MiB, in the private pilot state. It
uses `vhness-local-fixture`, a 10 MiB preservation budget, and a 64 MiB disk
reserve. The fixture-only transport never requests archive video and is rejected
for real or streaming collections. This is adapter evidence; the owner has additionally selected a two-episode Bill Nye sample, documented below. Democracy Now! remains streaming only.

The operator `acquire --file <manifest.json>` command requires `VHNESS_MEDIA` to
be an absolute preservation root; each collection uses its own subdirectory and quota. A manifest pins collection, item, complete
snapshot SHA-256, selected filename, byte count, optional expected SHA-256, and review
notes. Acquisition permission is separate from viewing permission. Transfers
check byte budget and disk reserve before and during reads; interrupted partials
remain for review. Resume requires a strong unchanged ETag and exact Content-Range.
There are no internal transfer retries. HTTP backoff returns the full deadline to
the operator; do not retry before it. Source MD5 and SHA1, when supplied, and optional expected SHA-256 must
match before an original is linked into immutable storage and bound to the catalog.

`audit --collection <local-id>` verifies every bound original against its recorded
receipt, including withdrawn originals. Serving checks review access before
conditional/Range handling and verifies file size/hash beneath `os.OpenRoot`.
This initial adapter serves compatible originals directly and retains all source
file metadata. It does not create remuxes/transcodes or acquire caption sidecars;
these require a separately budgeted processing/manifest extension before a real
collection needing them can qualify. Each local HTTP request hashes its original;
large-file performance has not been qualified.

`scripts/recovery.py --host nuc --destination /home/onnwee/Backups/vhness-private-pilot`
creates a consistent PostgreSQL custom dump, restores it to an isolated temporary
local database, transfers it to the independent host, then restores the transferred
copy in another isolated temporary database. Table fingerprints and exact raw
snapshot hashes must match. `--media <root>` additionally backs up and extracts
verified sample originals and receipts, capped at 10 MiB by default; `--media-budget` explicitly raises the bound up to 1 GiB. Recovery databases have
no network and are removed with their own disposable volumes after the drill.
Runtime/preview databases are never reset. Recovery receipts and archives stay
outside Git. The sample recovery command does not qualify browser playback.

The inventory driver is `scripts/metadata_inventory.py --hours 6` (maximum 12). It
reconciles search responses, runs bounded metadata-only calls, honors persisted
backoff, and backs up/restores after complete accounting. Its transient user unit
is `vhness-metadata-pilot.service`, limited to 768 MiB and one CPU. The current
qualification run uses a 12-hour driver with a 13-hour unit limit.
Inspect `systemctl --user status vhness-metadata-pilot` and `/api/status`; stop with
`systemctl --user stop vhness-metadata-pilot`. An incomplete bounded run requires
explicit resume. Coverage requires `discovery_complete`, queue total equal to
source count, no pending/retry entries, and explicit unavailable/rejected reasons.
The driver repeats discovery after the item queue finishes, so items added or
backfilled during ingestion enter the queue before its final coverage receipt.
Incomplete discovery continues from its retained page checkpoint.

API database operations have a three-second request deadline. Browser API fetches
have a ten-second deadline, combined with lifecycle cancellation. An unresponsive
database therefore produces explicit unavailable states and permits later channel
polls to recover. These deadlines do not limit direct source video or local file
streaming duration.

Back up before schema/runtime changes and after inventory completion; while the
pilot is active, run a daily backup and weekly independent restore drill. No daily
backup timer is installed yet. Retain seven daily and four weekly receipts as a
minimum; this workflow performs no automatic pruning. Preserve all source
snapshots, observations, editorial history, and schedule revisions. `/api/status`
reports growth and alerts at a 1 GiB database size; increase the metadata budget
or archive complete history to independently verified storage before exceeding
available disk. Pilot API logs use one retained file; inspect size during daily
review and rotate without deleting failure evidence. The bounded inventory unit
uses the host's existing journal retention.

## Browser qualification

Use native T3 preview tools on the isolated preview or private pilot URL. Record
actual target browser/device, original dates and replay labels, search/pagination,
remote playback/seek/AV, autoplay denial, one active player, and two simultaneous
channel sessions across a transition. Then block upstream source and verify
catalog availability plus an explicit streaming error. Local originals must
continue to play, seek, and transition with upstream blocked; repeat from recovered
metadata/media. Unit tests and decoder probes cannot substitute for these checks.
Native browser connection failures remain a qualification blocker until the
native transport works or the owner explicitly authorizes another browser.


## Approved Bill Nye sample

The owner selected `billnyethescienceguythecompleteseries-07-28-2021` and approved
only `01 Flight.mp4` and `02 Earth's Crust.mp4` (274,780,857 bytes total).
Collection `bill-nye` has a 512 MiB budget and 1 GiB free-space reserve; assets live
under the private pilot's `media/bill-nye`, with independent NUC recovery under
`/home/onnwee/Backups/vhness-private-pilot`. `scripts/bill_nye_pilot.py` checks the
exact source snapshot/file selection, imports two explicit file-based episodes,
records review and manifests, acquires unchanged source MP4 derivatives, and runs
integrity audit. It does not acquire the 100 AVI originals or the remaining MP4s.
Each normalized episode retains all 1,040 file entries, and the complete parent
response is stored once by SHA-256. Stable internal episode IDs remain separate
from the shared Archive item identifier. Source links and acquisition URLs use
that parent identifier. Individual episode broadcast dates are unknown; the
parent's series debut date remains in raw metadata.

The source provides MD5 and SHA1 rather than SHA-256. The manifest pins the
complete metadata snapshot containing those checksums; acquisition verifies both
and computes SHA-256 for immutable naming and recovery. A source with neither
verified source checksum nor an expected SHA-256 cannot be acquired.
