#!/usr/bin/env bash
# Checks the static Arkhiv product page image (site/) before it is deployed.
set -euo pipefail
image=${1:?image is required}
docker run --rm --network none --read-only --tmpfs /tmp:rw,noexec,nosuid,size=32m \
  --cap-drop ALL --security-opt no-new-privileges:true "$image" nginx -t
fixture=$(docker create --network none --read-only --tmpfs /tmp:rw,noexec,nosuid,size=32m \
  --memory 128m --memory-swap 128m --cpus 0.5 --pids-limit 64 \
  --cap-drop ALL --security-opt no-new-privileges:true "$image")
trap 'docker rm -f "$fixture" >/dev/null' EXIT
docker start "$fixture" >/dev/null
for _attempt in {1..20}; do
  if docker exec "$fixture" wget -q -T 2 -O /dev/null http://127.0.0.1:8080/healthz; then
    break
  fi
  sleep 0.2
done
html=$(docker exec "$fixture" wget -q -T 2 -O - http://127.0.0.1:8080/)
grep -Fq 'Seven archives with their own names.' <<< "$html"
grep -Fq 'href="https://arkhiv.subcult.tv/"' <<< "$html"
grep -Fq 'name="twitter:card" content="summary_large_image"' <<< "$html"
for site in anarchist-archive riot-grrrl cla lineage dedhed bill-nye democracy-now; do
  grep -Fq "href=\"https://$site.subcult.tv/\"" <<< "$html"
done
# Cloudflare rewrites email addresses in HTML, so the live page would no longer
# match the image and the Almaz receiver would roll the deploy back.
for page in / /404.html; do
  if docker exec "$fixture" wget -q -T 2 -O - "http://127.0.0.1:8080$page" 2>/dev/null \
    | grep -Eq '[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]'; then
    echo "$page must not contain an email address" >&2
    exit 1
  fi
done
for path in favicon.ico favicon.svg apple-touch-icon.png og.png site.css fonts/newsreader-700.woff2 fonts/public-sans-400.woff2; do
  docker exec "$fixture" wget -q -T 2 -O /dev/null "http://127.0.0.1:8080/$path"
done
# Retired catalog paths answer 404 with the page's own notice, not the old interface.
if docker exec "$fixture" wget -q -T 2 -O /dev/null http://127.0.0.1:8080/documents/retired-catalog-path 2>/dev/null; then
  echo "unknown paths must return 404" >&2
  exit 1
fi
