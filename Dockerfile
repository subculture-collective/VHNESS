FROM node:24-bookworm-slim AS frontend
RUN npm install --global pnpm@10.33.0 --force
WORKDIR /src
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY web/package.json ./web/package.json
COPY packages/viewer/package.json ./packages/viewer/package.json
RUN pnpm install --frozen-lockfile
COPY web/ ./web/
COPY packages/ ./packages/
RUN pnpm --dir web build

FROM nginx:1.28-alpine AS reference-frontend
COPY --from=frontend /src/web/dist /usr/share/nginx/html
COPY deploy/reference-nginx.conf /etc/nginx/nginx.conf
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=20s --timeout=5s --start-period=10s --retries=3 CMD ["wget", "-qO-", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["nginx"]
CMD ["-g", "daemon off;"]

FROM golang:1.26-bookworm AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
ARG CORE_VERSION=dev
ARG SOURCE_REVISION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.coreVersion=${CORE_VERSION} -X main.revision=${SOURCE_REVISION}" -o /out/vhness ./cmd/vhness

FROM debian:bookworm-slim AS core
# Keep tool dependencies in separate layers for registries with bounded uploads.
# Debian still resolves and tracks every runtime dependency through apt.
RUN apt-get update && apt-get install --yes --no-install-recommends ca-certificates poppler-utils util-linux && rm -rf /var/lib/apt/lists/*
RUN apt-get update && apt-get install --yes --no-install-recommends libllvm15 && rm -rf /var/lib/apt/lists/*
RUN apt-get update && apt-get install --yes --no-install-recommends libgl1 && rm -rf /var/lib/apt/lists/*
RUN apt-get update && apt-get install --yes --no-install-recommends libavcodec59 && rm -rf /var/lib/apt/lists/*
RUN apt-get update && apt-get install --yes --no-install-recommends libavfilter8 libavdevice59 && rm -rf /var/lib/apt/lists/*
RUN apt-get update && apt-get install --yes --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*
COPY --from=backend /out/vhness /usr/local/bin/vhness
COPY migrations/ /app/migrations/
ENV API_ADDR=0.0.0.0:8080 VHNESS_MIGRATIONS=/app/migrations ARKHIV_COMMUNITY_MIGRATIONS=/app/migrations/community VHNESS_MEDIA=/media
USER 1000:1000
EXPOSE 8080
HEALTHCHECK --interval=20s --timeout=5s --start-period=20s --retries=3 CMD ["vhness", "healthcheck"]
ENTRYPOINT ["vhness"]
CMD ["serve"]

# Optional bundled reference interface. Core builds stop before this stage.
FROM core AS reference
COPY --from=frontend /src/web/dist /app/web
ENV VHNESS_WEB=/app/web
