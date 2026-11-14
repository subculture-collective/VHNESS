# Public deployment by dispatch

`deploy-public` is a `workflow_dispatch` in this repository that runs on Almaz's
own Gitea runner (label `almaz`). It resolves a plan with
`scripts/deploy_plan.py` and hands it, over SSH, to a forced command on Almaz
that runs `scripts/public_deploy.py` as the deployment owner. Design and
decisions: [plan 0006](../plans/0006-automated-update-and-deploy.md).

## What a run does

1. The plan names, per compose service, the image that the owning repository
   published for its current `main` commit (`<registry>/<name>:<40-hex>`), and
   for each shared core the immutable digest that the owning consumer pins in
   `deploy/archive-core.env` at that same commit. A core never moves ahead of
   the consumer that verified it.
2. On Almaz the script takes `~/.local/state/arkhiv-deploy/deploy.lock` and,
   for the platform target, the maintenance lock the monitor, replay and backup
   jobs share. It pulls every image, refuses any whose
   `org.opencontainers.image.revision` label differs from the planned revision,
   and derives brand frontends that must proxy a differently named core
   (`democracy-core`, `bill-core`) from the consumer image.
3. If a core or API image changes, it runs the configured backup first, then
   each core's `migrate` with the private operator credential. With
   `ARKHIV_READER_ROLE` set on that command, `migrate` grants the public role
   SELECT on every table, including new ones; see
   [public core](../reference/public-core.md).
4. Each changed service is swapped through a compose overlay that changes only
   the image: configuration equivalence is checked with `compose config`,
   `up --wait` runs for that one service, the new container must keep the old
   environment, host limits and mounts, and the public origin must answer 200
   with the expected body. Any failure restores that service's previous image
   and stops the run; earlier services stay on their new images and the receipt
   says so.
5. The configured `after` command (the public monitor) runs last.

Receipts, overlays and rollback overlays live under
`~/.local/state/arkhiv-deploy/deploys/<timestamp>-<target>/`. Previous images
are never deleted. `--dry-run` pulls, verifies and reports without swapping.

## Host installation (Almaz, as `onnwee`)

```sh
install -d -m 700 ~/.local/state/arkhiv-deploy
install -m 600 scripts/public_deploy.py ~/.local/state/arkhiv-deploy/public_deploy.py
install -m 600 deploy/public-deploy.example.json ~/.local/state/arkhiv-deploy/deploy.json   # then edit paths
install -m 700 deploy/arkhiv-deploy-entry.sh ~/.local/bin/arkhiv-deploy-entry
```

Add the deploy key to `~/.ssh/authorized_keys` with the forced command and no
other capabilities:

```
command="/home/onnwee/.local/bin/arkhiv-deploy-entry",restrict ssh-ed25519 AAAA... arkhiv-deploy
```

Store the private half as the repository secret `ALMAZ_DEPLOY_KEY`. The job
pins Almaz's host key from `deploy/almaz.known_hosts`. Rotate by replacing the
`authorized_keys` line and the secret together; the old key stops working at
once.

Updating the host script is a reviewed step: copy the merged revision of
`scripts/public_deploy.py` over the installed copy. Every receipt records the
SHA-256 of the script that ran.

## Rollback

Each receipt lists, per service, `rollback_command`: the same `compose up`
with that service's `*.rollback.json` overlay, which restores the previous
image id. Run it as `onnwee` on Almaz for the services to revert, newest first.
Database migrations are additive and checksum-locked; a core rollback must use a
compatible checkpoint when a migration has run, as
[deployment operations](../DEPLOYMENT.md) already requires.
