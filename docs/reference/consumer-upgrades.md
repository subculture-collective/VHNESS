# Reviewed consumer upgrades

[consumer-registry.json](consumer-registry.json) selects repositories and dependency
files managed by the shared platform. `platform` consumers pin the package,
release receipt and shared engine together. Explicit `components` consumers pin
the package and its release provenance while retaining their own backend. Their
registry entry must omit `engine_file`; an ambiguous mode or engine pin is rejected.
A release receipt records the upstream artifact, not a claim that its engine is
running in a component consumer.

`scripts/upgrade_consumer.py` prepares an upgrade in a clean isolated checkout.
It requires the release manifest and the packed registry artifact. The manifest
must come from a successful reviewed release; checksum validation binds the
artifact to that receipt, but does not independently establish its publisher.
It rejects unsupported APIs/media/configuration, changed registry ownership,
major upgrades, downgrades, reused versions and changes to existing migrations.
The package dependency, pnpm lock and release receipt change together. Platform
consumers also update their exact engine digest. Consumer checks run before
proposal creation. Failure restores every managed file; use a disposable checkout because checks may create build outputs.
The checks require a task-owned disposable database when `make verify` does.

`scripts/propose_consumer_upgrade.py` clones only the selected registered consumer
and prepares the upgrade. It uses `ARCHIVE_UPGRADE_TOKEN` for Gitea API requests
and verifies the account is `subcult-agent`. Provision a separate repository-write
token for this job; the package publishing token cannot create proposals. Git
uses the installed agent HTTPS credential helper. Registry authentication must
be supplied through an ephemeral npm configuration outside the repository.

The branch name binds the version and complete release source revision. Repeated
runs reuse the existing PR after comparing its release receipt, including PRs
beyond the first API page. A declined PR is not reopened, and an already applied
release produces no proposal. Git pushes never force-update an existing branch.
If a push succeeded but PR creation failed, retain the branch and inspect it
before retrying; do not delete it to make the retry succeed.

The tools do not merge or deploy consumers. Local fixture tests cover guards,
checksum failure, file restoration, repeat preparation and proposal reuse.
Hosted PR creation, installation from the registry, consumer CI, rendered private
qualification and effective deployment revisions remain separate receipts.

## Initial adoption and stacked proposals

The neutral video and Democracy Now! adoption branches consume the published
0.1.0 package. Their exact revisions, merged PRs and merge revisions are recorded in the
registry. They now consume the package on their default branches; deployment
adoption remains separate. Both passed isolated registry installation, consumer verification and
image builds over the qualified engine digest.

When an adoption PR is still open, pass `--base-ref feat/shared-package-adoption`
to create the next upgrade as a separate stacked PR. The tool checks out that
remote branch and verifies its actual pins before mutation. Repeat proposals
must match the requested base and release receipt. After adoption merges,
retarget the stack to the default branch and update the registry evidence; an
existing PR with a different base requires explicit reconciliation. Normal
upgrades omit `--base-ref` and use the default branch.

## Hosted proposal automation

Since 2026-11-14 the proposal job is the GitHub workflow `Consumer upgrades`.
The `Release receipts` workflow dispatches it for each receipt it lands (a merge
made with the Actions token starts no push workflow), and a receipt merged by a
person starts it through the push trigger; it can also be dispatched with a lane
and version. It clones each registered consumer from GitHub (`github_repository`
names the GitHub copy when the registry keeps an older Gitea path), runs the
consumer's checks without a test database, pushes the upgrade branch and opens
the PR with the `ARCHIVE_CONSUMER_TOKEN` repository secret, a fine-grained token
with contents and pull-request write access to the consumers. Each consumer
merges a ready PR once its own required checks pass; a failed check leaves it
open for review. The job never deploys a consumer; a merged upgrade deploys
through that consumer's own pipeline. The design and rollout order
are in [plan 0006](../plans/0006-automated-update-and-deploy.md).

Committed receipts under `docs/releases/` bind published registry bytes to their
qualified source and engine digest. The proposal job runs on default-branch
receipt/registry changes or explicit dispatch. It downloads the actual registry
package, verifies the receipt checksum, and prepares consumer PRs with the pinned
pnpm toolchain. Package downloads cannot forward credentials to another registry.

CI uses separate package-read and repository-write credentials. Its Git helper
serves only the agent identity and registered consumer HTTPS paths. The job
checks compatibility and consumer builds before committing; repeat runs reuse
the matching proposal. It never merges or deploys. A dispatch with
`adoption_refs=true` stacks on the exact qualified adoption revisions. A moved
adoption branch is rejected until its evidence is refreshed.

Version 0.2.1 passed hosted release run 12652 at source
`04eb1f80a0e5d5bc92f83c0030204cafee131d96`. Its registry tarball matched the
hosted receipt; the engine digest is recorded in `docs/releases/release-0.2.1.json`.

Qualified release runs: 0.1.0 is VHNESS run 12470, source `d2a005f0ecd2a81614f1009abf2473832cc1afaf`;
0.1.1 is run 12509, source `8827514b215162e5536f2b83a65681ba70e38f30`.
Both completed every hosted release step successfully, and their registry
tarballs match the downloaded hosted receipts. Consumer deployment and browser
qualification remain separate.

The first hosted proposal attempt (run 12522) failed safely because both
adoption branches were merged and removed while the job was starting. No
upgrade branch or PR was created. The registry now records both merged
adoptions, so subsequent proposals use their default branches.

## Verification isolation for print and audio consumers

The hosted proposal job installs the Go toolchain and the media tools needed by
registered backend checks. Its PostgreSQL service carries the exact job identity.
The disposable server and Ubuntu verification client both use PostgreSQL 16;
print backup/restore tests invoke `pg_dump`, which refuses a newer server major.
This CI pairing does not change any brand's runtime database version.
Each consumer gets a fresh database named for that consumer; the tool verifies
the container label, disposable launcher identity and address before creating it
and supplying `TEST_DATABASE_URL`. The `db` alias must resolve to that verified
job-owned address. The service uses the launcher's `dev` user and disposable
password so consumer integration guards remain intact. A missing or conflicting container identity fails the job.
This keeps migration and database tests separate across brands. Local proposal
work must use the shared isolated launcher for the same required gates.

## Independent viewer upgrades

The six current frontend consumers now register mode `viewer` and `deploy/viewer-release.json`. Their previous combined registrations remain in `previous_registration`; historical adoption links retain their original dates and repository names. The Anarchist consumer uses its canonical `arkhiv-anarchist` repository name, with the former name retained as provenance. API-only consumers remain a separate lane.

Use `scripts/propose_release_upgrades.py --kind viewer --version 0.4.2` for the committed SDK-only receipt, or pass that receipt and its verified tarball directly to `upgrade_consumer.py` or `propose_consumer_upgrade.py`. The default proposal lane is now `viewer`. The owner-waived hosted job remains separate from local qualification; these source changes do not activate it while Kvant is unavailable.

Before mutation, the tool validates the package identity, registry, license, stable version, complete source revision and checksum. The SDK's named compatible core must match the consumer's retained reviewed core version or a committed older core receipt whose contracts and migration checksums remain compatible with the newer core. Major core changes, missing receipts and changed migrations fail before mutation. The SDK's original qualification receipt stays unchanged. The retained core receipt must support version-1 API/configuration and all registered media/modules, and its exact immutable image pin must match `core_engine_file`. This receipt check does not claim that a native backend has completed its separately reviewed shared-core cutover.

The SDK lane changes only the package dependency, frozen pnpm lock and viewer receipt. It does not manage `engine_file`. Registered core files are read-only inputs; if consumer checks alter them, preparation fails and restores the original registered files. Failed checks restore the SDK files and lock as well. Major upgrades, downgrades, reused versions, checksum errors, missing/escaping/overlapping registered paths and incompatible cores fail before delivery. Successful preparation runs each consumer's required checks before creating a proposal.

SDK proposals use `archive-viewer-upgrade/v<VERSION>-<SOURCE>` branches. They keep the existing exact-receipt reuse, declined-proposal refusal, explicit stacked-base selection and timeout reconciliation. An already applied SDK produces no file changes or PR. Backend releases continue through the independent core lane. Legacy combined manifests require an explicitly registered combined consumer; they are not substituted for an SDK-only receipt.
