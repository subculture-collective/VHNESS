# Headless Arkhiv core

Arkhiv is an HTTP archive core. End users choose the entire frontend, its framework,
navigation, layout, controls, styling and hosting. Neither React nor
`@subcult/archive-viewer` is required to use the core. The neutral reference app
and shared components/hooks are optional clients of the same APIs.

## Build and run independently

`docker build --target core -t arkhiv-core:SOURCE .` builds only the Go engine and
its media/tool runtime. The core target contains no `/app/web` and does not build
the Node/frontend stages. It exposes the same `vhness` operator and `serve`
commands, migrations and media adapters. `VHNESS_WEB` is unset; `/` and arbitrary
frontend paths return 404. Existing default image builds select the final
`reference` stage, preserving compatibility for bundled apps.

`make verify-core` runs formatting, vet, race-enabled backend/database tests and
compilation without Node or a frontend package. `make verify` additionally checks
all optional clients and the reference app. Database tests always require the
explicit task-owned disposable `TEST_DATABASE_URL`; core independence does not
relax that guard.

Runtime still needs its own database, reviewed collection scope, credentials and
appropriate read-only media mounts. Follow the existing deployment and recovery
runbooks. Building this target does not publish a new release or deploy it.
Issue #86 tracks independent immutable core publication and qualification;
already published 0.2.2 artifacts remain unchanged.

## Plain HTTP integration

Any HTTP client can begin with `GET /api/version` for API version, core release
(`core_version`), source revision (`revision`), enabled module versions and the
configured collection scope. Liveness is `/api/health`, which also returns
`revision`; `/api/ready` checks database readiness. Neither requires a frontend.
Release builds set the core version and revision through the `CORE_VERSION` and
`SOURCE_REVISION` Docker build arguments; a build without them reports `dev`.

| Capability | HTTP contract |
| --- | --- |
| Browse collections and mixed items | `GET /api/catalog/v1/collections`, `GET /api/catalog/v1/items`, `GET /api/catalog/v1/items/{id}`, `GET /api/catalog/v1/creators` |
| Resolve a reviewed representation | `GET /api/catalog/v1/items/{id}/representations/{representation}/resolve` |
| Read a local original or permitted download | The resolver's access-checked media URL, including Range requests |
| Print status, search and cover | Representation `/print`, `/search` and `/cover` endpoints |
| Continuous audio | Representation `/audio/resolve` and its reviewed `/audio/media` URL |
| Shared audio station | `GET /api/audio/channel?collection=COLLECTION` |
| Existing video integrations | `/api/episodes`, `/api/episodes/{id}`, `/api/playback/{id}` and `/api/channel` |

For example, a frontend or command-line client requests
`/api/catalog/v1/items?collection=COLLECTION&media=audio&page=1`, displays the
returned item and representation identities, then resolves its selected reviewed
representation. The consumer chooses how to render tracks, pages or controls.
[Mixed catalog](mixed-catalog.md), [access reviews](representation-access.md),
[print](print-module.md) and [audio](audio-module.md) document the payloads,
query parameters and specialized behavior. Existing video endpoints remain
compatible; clients can use them without adopting the mixed reference UI.

The API enforces collection scope and independent read/play/download/scheduling
reviews for every client. A listed item grants no transport permission. Denied or
withdrawn representations return 404; operational failures remain explicit.
Source-stream media resolves directly to its source, and the core refuses to
proxy it. Local media must match its approved preservation manifest. Clients
must preserve source identity and display server uncertainty rather than infer
rights, set boundaries or source replacements.

A frontend can live on a separate server while a reverse proxy exposes `/api`
under its origin. This is the currently supported browser topology. Direct
cross-origin browser deployment needs an explicitly reviewed origin policy;
the core currently does not enable wildcard CORS. Server HTTP clients do not
require CORS. Administrative ingestion, review, migration and scheduling remain
operator commands with database authorization, rather than public web mutations.

## Updates and ownership

Core policy, metadata, resolution and scheduling fixes reach compatible clients
through a reviewed backend upgrade. Backend upgrades do not replace end-user
markup or styles. Optional React package fixes reach only package consumers after
their own upgrade. API compatibility and schema migration/recovery determine
upgrade order; frontend design is outside that contract.

The existing six-consumer registry demonstrates supported integrations. Component
consumers retaining their own backends share optional frontend code only; adopting
those components does not mean they run the Arkhiv core. Future API-only consumers
record core image and API compatibility separately, without React package pins.
Register these with `mode: api`, `adoption: core`, `release_file`, `engine_file`,
enabled `media`/`modules`, `rollout: reviewed-pr` and independent `checks`.
The core release receipt contains its source, immutable image digest, API/module
versions and migration checksums; it has no frontend package fields.

`scripts/propose_release_upgrades.py --kind core --version VERSION` proposes only
API consumers. It verifies compatibility and locked migration history, updates
the core pin and receipt, runs the consumer checks and opens a reviewed PR.
It does not install frontend dependencies or change frontend source. Failed checks
restore both pin files. The separate `core-release.yml` workflow verifies and
publishes the `core` target to `subculture-collective/archive-core`; the reference
image and component package keep their own release stream. Issue #86 requires
actual hosted publication and consumer/runtime evidence before qualification.

## Separate reference deployment

`deploy/headless-compose.yml` runs three services: PostgreSQL, the independently
released core, and a static frontend built with Docker target
`reference-frontend`. The frontend proxies `/api/` to the core on its private
network. The API can also have its own public hostname through the core's
`arkhiv-platform-core` network alias; the frontend uses
`arkhiv-platform-reference`.

`deploy/core-release.json` records core 0.1.0 and its published immutable image
digest. `deploy/archive-core.env` selects that image. Run
`python3 scripts/check_core_pin.py` to check the pair. A core upgrade changes
these two files and runs backend checks; it does not rebuild or replace the
frontend image. Build and pin the frontend separately with
`docker build --target reference-frontend`.

Before starting this Compose project, create its own `deploy/runtime` directory
with a database password file, a database URL file, `core.env`, and a media
directory. Set `DATABASE_URL_FILE=/run/secrets/database-url` in `core.env`, along
with the reviewed collection scope. Secret files stay outside Git; make the URL
file readable by core UID 1000. The frontend image pin is supplied separately as
`ARKHIV_REFERENCE_IMAGE`. Supply both environment files explicitly to Compose.
Apply migrations to this project's database before starting the core.

This configuration is prepared for public rollout. Image publication and
configuration checks alone do not establish public routing, reviewed catalog
access, recovery, or browser playback. Record those checks for the deployed
revision before declaring rollout complete.

[Reviewed public deployment](public-core.md) describes the separate public audience and operator approvals in core 0.1.1.

For public instances, keep the database and core on the instance's internal
archive network. Attach only the frontend/API proxy to the ingress network;
route the API hostname through that proxy's `/api/` path. Select an unused
`ARKHIV_ARCHIVE_SUBNET` after inspecting host routes and Docker IPAM. This avoids
exhausted default pools and keeps the generic `archive-core` DNS alias confined
to its own instance. Use `ARKHIV_PUBLIC=1`, a nonempty collection scope and a
read-only database role in `runtime/core.env` after migrating and recording
explicit publication reviews with the separate operator identity.
