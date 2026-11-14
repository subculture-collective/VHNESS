# The deployable core: the archive-core image pinned in deploy/archive-core.env,
# relabelled for the fleet-deploy receiver. Labels only; never add layers here.
# CI passes CORE_IMAGE from deploy/archive-core.env and checks the built image's
# layers against the pinned digest (scripts/check_core_pin.py checks this file).
ARG CORE_IMAGE
FROM ${CORE_IMAGE}
# REVISION is this repository's commit; CORE_VERSION and CORE_REVISION come from
# deploy/core-release.json. The receiver's /api/version check compares
# core_version with org.opencontainers.image.version.
ARG REVISION
ARG CORE_VERSION
ARG CORE_REVISION
LABEL org.opencontainers.image.source="https://git.subcult.tv/subculture-collective/arkhiv" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${CORE_VERSION}" \
      org.subcult.archive-core.revision="${CORE_REVISION}"
