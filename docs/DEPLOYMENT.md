# Engine and product deployments

The engine image includes a configurable white-label frontend. Democracy Now!
and Bill Nye have independent frontend repositories and images, pinned to a
shared engine release. Each deployment owns its database, credentials, worker
checkpoints, decisions, replay schedule, and backups. Compose binds the viewer
to loopback. Public ingress remains a separate owner decision.

## Artifacts and configuration

Run `make verify` with a disposable integration database, then build from a
clean committed revision: `docker build -t vhness-engine:SOURCE_SHA .`. Record
the source SHA, image ID/digest, and checksums of exported image archives. Keep
the previous image and database checkpoint before upgrading.

App repositories contain generated viewer adapters and manifests. Their source
belongs to the engine. Export a clean reviewed revision with
`python3 scripts/export_viewer.py --destination /absolute/app/web/src/engine`,
verify hashes, and qualify both app viewing modes before an upgrade. Do not
edit generated adapters in an app repository. Layout, tokens, assets, and CSS
belong to each product.

Prepare a dedicated private root outside Git and a unique Compose project:

```sh
python3 scripts/deploy.py prepare \
  --root ~/.local/state/video-archive/white-label \
  --project white-label-archive --image vhness-engine:SOURCE_SHA \
  --collection democracy_now_vid --port 48071
python3 scripts/deploy.py start \
  --root ~/.local/state/video-archive/white-label \
  --project white-label-archive \
  --snapshot /absolute/reviewed/democracy-now.dump
```

The helper creates private credentials without printing or overwriting them.
Snapshot restore is allowed only when the owned database has no public tables.
Starting without a snapshot creates an empty catalog; content import/review
is still required. Readiness proves database access, not content or playback.

The image runs as a non-root user. Compose uses a read-only app filesystem and
media mount, bounded temporary space, dropped capabilities, CPU/memory limits,
and a database volume without an exposed host port. `prepare` records the
operator UID/GID for owned files. Keep secrets outside Git and never reuse a
pilot password in another product.

For the white-label app, copy `web/public/site.json` outside Git and change its
name, subtitle, collection, headline, description, and six color tokens. An
optional seventh token, `label`, colors small labels; without it they use the
accent. Supply
`--brand /absolute/site.json` to `prepare`. Optionally provide `--brand-assets
/absolute/assets` and a logo path such as `/brand/logo.svg`. The mounts are
read-only. Configuration accepts bounded text, hex colors, and local asset
paths. The collection must match the engine instance. Check rendered text and
focus contrast after changing tokens; valid syntax does not prove accessibility.

The default identity is Arkhiv's Studio pack: its palette, Newsreader, Public
Sans and IBM Plex Mono (served from `web/public/fonts/`), and the mark in the
header. See `brand/README.md`.

## Operator commands and schedules

Use the same owned environment/image for operations:
`docker compose --env-file ROOT/deploy.env -p PROJECT -f deploy/compose.yml run
--rm --no-deps app COMMAND`. Collection scope is enforced on CLI actions and
viewer API routes. Metadata workers request metadata/search only, never video.

`review-library --items 1000 --after-id CURSOR --notes AUTHORIZATION` reviews
bounded batches of previously unreviewed streaming episodes. It prefers eligible
480p MP4s, records source snapshots/selected files/skips, and preserves existing
decisions. This explicit private-library review does not grant public release
or prove decoding of every file.

`plan-replay --days 7` creates or extends a bounded replay schedule using reviewed
eligible episodes. It retains unexpired program slots, appends after the existing
horizon, and saves a new immutable revision. Renew before the horizon expires;
exhaustion remains explicit. Source refresh/review and horizon renewal are
operator tasks; these commands do not install unattended host timers.

Source captions require explicit decision tracks with `file`, `label`, and
`language`. Discovery does not enable them. Browser fetches have a ten-second
deadline and 2 MiB track limit, then transient Blob URLs for native WebVTT.
Video remains a direct normal source request. Source caption CORS must work;
caption failure is visible without breaking video. Blob URLs are revoked on
player removal. Local caption preservation requires a separate manifest/budget.

## Preservation and recovery

Democracy Now! stays permanently streaming-only, including in the white-label
app. It has no application-managed acquisition, video cache/proxy, transcode,
or offline fallback. All metadata, files, unknown fields, observations, review
history, and schedule revisions remain stored.

The owner expanded Bill Nye preservation on 2026-10-03 to all 100 source MP4s,
13,763,456,996 bytes, after primary and independent capacity checks. The reviewed
budget is 16 GiB with a 1 GiB reserve. Originals stay under
`~/.local/state/vhness/private-pilot/media/bill-nye`; independent backups stay
under `/home/onnwee/Backups/vhness-private-pilot` on NUC. AVI duplicates are not
acquired. `review-budget` requires matching previous limits and authorization
notes, records budget history, and leaves policy/source identity unchanged.
Implicit changes remain rejected.

`scripts/recovery.py` supports an explicit media bound up to 32 GiB, streams
checksum calculation, and verifies restored tables and raw source bytes on the
independent host. `scripts/export_collection.py` derives a product-only dump
from a disposable restored copy. It does not alter original metadata or media.
It scopes only the metadata, decision, schedule and snapshot tables; when the
restored copy has rows in any other table, it refuses and names them rather than
shipping them unfiltered under a collection-only receipt.
Keep source/export checksum receipts with release artifacts.

Back up before migrations. Rollback uses the previous verified image and its
compatible checkpoint in a new owned deployment, followed by readiness,
catalog, playback, schedule, and restore checks. Never reset a live database or
assume an old image is compatible with a newer schema.
