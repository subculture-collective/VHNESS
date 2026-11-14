# Shared platform release operations

The shared viewer is built and checked through the root pnpm workspace.
`make verify` includes a fresh offline consumer installation of the packed ESM
artifact, as well as the Go/disposable-database and frontend checks.

Registry targets and compatibility are maintained in [release-contract.json](release-contract.json).
Gitea npm packages are installed from its organization registry; engine releases
use immutable container digests. See the official [npm registry documentation](https://docs.gitea.com/usage/packages/npm/).

The manual/tag release workflow requires `ARCHIVE_PACKAGE_TOKEN`, a token with
package read/write permission supplied through Gitea Actions secrets. Use the
existing secret provider for its retained recovery copy. Do not put the operator's
broad repository credential into the workflow. Gitea documents the built-in job
token's [package publishing limitation](https://docs.gitea.com/usage/actions/comparison/).
The separate consumer-upgrade automation needs its own repository-write credential;
package publishing permission is not a reason to grant cross-repository writes.
Set the repository variable `ARCHIVE_REGISTRY_USER` to the token's actual account
name; an organization registry owner is not necessarily a login identity.

The release verifies with a job-owned database, builds/pushes the engine, records
its registry digest, binds package bytes and migrations to the source revision,
then publishes the package. It never deploys a consumer. A failed publish is not
a qualified release; inspect registry state before retrying. Existing package
versions are immutable. Bump the version for changed bytes, preserve previous
artifacts, and never delete a version to make a retry succeed.

`scripts/release_manifest.py` requires clean committed source, a registry digest,
matching packed package metadata/bytes, and a new output destination. A local
image ID or mutable tag is rejected. The release receipt records migration hashes,
API/configuration compatibility, software license and source revision. Readers
must still qualify consumer behavior; a manifest is not a deployment receipt.

Retain the current and previous reviewed release during staged adoption. Review
breaking APIs/configuration and coordinate engine/migration/client order. Rollback
must use a compatible schema or an independently restored checkpoint. Public
release and collection approvals remain independent of package publication.

## Automated releases

The Gitea workflow `viewer-release` publishes `@subcult/archive-viewer` when a
merge on `main` changes the version in `packages/viewer/package.json`, after the
full `make verify` gate with a job-owned database. `core-release` does the same
for `docs/reference/core-version.json` changes (tags still work) and pushes the
core image. A version that already has a committed receipt is skipped; a viewer
version already in the registry without a receipt stops the job for an operator.

Gitea pull requests have been off since 2026-11-10, so the receipts land on
GitHub. After the same version bump, the GitHub `Release receipts` workflow runs
`scripts/land_release_receipts.py`. It waits up to 150 minutes for the published
artifact, reading the registries anonymously. For the core image it accepts the
bump commit or a later `main` commit whose image labels name that revision and
version. It then writes the receipt with `scripts/core_release.py` or
`scripts/viewer_receipt.py` (the viewer receipt keeps the previous compatible
core) and opens a `release/receipts-<sha>` pull request with commits authored
by `github-actions[bot]`. Because its token starts no workflows, it dispatches
Mirror CI on that branch; GitHub does not link a dispatched run to the pull
request, so when that run passes the job records it as the required `Mirror CI
gate` check on the branch head and enables auto-merge. Each merged receipt starts the consumer proposals described in
[consumer upgrades](consumer-upgrades.md).

## Independent SDK patches

Viewer 0.4.1 is an SDK-only patch with `docs/releases/viewer-release-0.4.1.json`. It changes no core or reference-engine image. Keep the consumer's reviewed core and engine receipts separately; validate the exact frontend tarball/source/checksum against its SDK receipt. Do not replace the reference-engine digest with a nonexistent 0.4.1 image or label the retained engine as a 0.4.1 release. The six consumers now verify SDK provenance independently of their core/reference-engine provenance. The existing full-platform upgrade command expects a combined platform receipt; this audit's SDK-only adoption uses independently reviewed consumer patches rather than passing the SDK-only receipt into that command. Existing scripts continue to support their documented combined/core receipt formats.

No artifact is withdrawn by this patch. Keep current and prior qualified SDK/core/engine receipts during staged review. Breaking releases still require compatibility/order and independent checkpoint recovery; product frontends and optional community-provider/public activation remain separately owned. See the [October 6 qualification matrix](../reports/cross-brand-qualification-2026-10-06.md).

The later October 6 release-tooling follow-up adds an independent `viewer` lane for SDK-only receipts. The current six-consumer registry uses that lane; the prior combined registration remains historical. See [consumer upgrades](consumer-upgrades.md) for compatible-core validation, the three managed SDK files, rollback and idempotent proposal behavior. This supersedes the earlier tooling limitation for future qualified upgrades, without changing existing engine/core artifacts.
