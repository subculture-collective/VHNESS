# Reviewed public core deployment

Core 0.1.1 adds an explicit public audience. Set `ARKHIV_PUBLIC=1` and a
nonempty `VHNESS_COLLECTION` or `VHNESS_COLLECTIONS` allowlist for `serve`.
An invalid audience setting or unrestricted public scope fails startup. The
private audience remains the default. `/api/version` reports the audience.

Public approval is separate from private catalog listing, playback, acquisition
and storage decisions. Migration 0009 approves no content. Existing approvals
and histories remain in place. Apply migrations with the private operator
identity before starting a public server with its own read-only database role.
No HTTP endpoint can approve content.

Set `ARKHIV_READER_ROLE` to that role's name (for example `arkhiv_reader`) when
running `migrate`. After applying migrations, `migrate` grants the role USAGE on
the migration schema (`public` in deployments) and SELECT on every table and
view in it, including tables created in the same run, so a release that adds
tables needs no manual `GRANT`. The role must already exist; an unknown or
malformed name fails before any migration is applied. Without the setting,
`migrate` changes no privileges.

For native video entries, record the owner decision with:

```sh
vhness review-public-video --id EXACT_EPISODE_ID --approve-public \
  --notes 'Exact owner authorization and rollout scope'
```

The approval binds the current source snapshot and selected file and requires
existing playable permission. Run the same command without `--approve-public`
and with withdrawal notes to revoke publication. Changing the snapshot or
selected file invalidates approval; history remains recorded. This independent
record does not change the historical `decision.public` field.

For common print, audio or video representations, prepare the existing bound
transport review with `review.public=true`, retaining the exact files, permitted
actions, source snapshot and any local preservation/recovery manifest. Apply it
through `review-transport --approve-public`. The ordinary private review path
rejects a public approval payload. Publication adds no playback, download,
acquisition, schedule or relay permission. Source-only Democracy Now! restrictions
still apply. A normal private review can withdraw public access.

The public audience filters catalog entries, collection counts, native video
lookups, source playback, local media and representation transport. A shared
video schedule containing an unapproved program is unavailable publicly, so its
future private program identifiers are withheld. Audio schedules also require
current public transport approval for every program. Operator queue/status
information is unavailable at the public `/api/status` route.

Use an independently pinned frontend and proxy to the public core. Record actual
public catalog, media, denied-content and recovery checks for the deployed
revision before declaring rollout complete. The integration checks cover
private-content denial, explicit print/video approval, exact original bytes,
withdrawal, changed-source invalidation, private future schedules and unchanged
private catalog visibility; they do not establish real media fidelity.

## Automatic collection access

A collection can also carry one recorded automatic access decision, as the
retained Anarchist collection did with its `approve-automatic` policy. Migration
0024 adds the decision tables and records no decision. Record or repeat one with:

```sh
vhness review-public-collection --collection COLLECTION --approve-public \
  --notes 'Exact owner decision and scope'
```

The command records the decision and applies it in one transaction. It publishes
listed print representations whose current private review grants reading of the
current source snapshot, including the primary file, and whose primary file has
a reader index with at least one page. Each grant keeps the review's files,
actions and preservation record, passes the same checks as `review-transport
--approve-public`, and is linked to the decision. The command prints the decision
ID, the number approved and each skipped representation with its reason. Apply it
after importing, reviewing and indexing; run it again to cover representations
reviewed later. Each run is a new recorded decision.

To withdraw one item, record its normal private review with `review-transport`.
Any private review recorded after a public grant counts as a withdrawal, and
later automatic decisions skip that representation. Only a manual
`review-transport --approve-public` makes it public again.

Run the command without `--approve-public`, with withdrawal notes, to withdraw
the decision. It revokes only the grants that automatic decisions made; manual
approvals stay public. Withdrawing the decision does not count as withdrawing
the items, so a later automatic decision can publish them again.

Transport history marks each row as `operator` or `automatic` and links automatic
rows to their decision:

```sh
vhness transport-history --id ITEM --representation REPRESENTATION \
  --collection COLLECTION
```
