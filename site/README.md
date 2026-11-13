# Arkhiv product page

The static page served at <https://arkhiv.subcult.tv/>: what the engine keeps,
how releases work, and links to the seven archives that run on it. It follows
the Diagram layout selected on 13 November 2026; the reviewed mockup and the
website rules are in Studio (`studio/brands/arkhiv/WEBSITE.md`).

- `public/` is copied as-is into the image. There is no build step and no
  JavaScript; light and dark follow the visitor's system setting.
- Fonts are the Studio pack's Newsreader 700, Public Sans 400/700 and IBM Plex
  Mono 400, with their OFL licences beside them.
- Facts in the copy come from this repository and each archive's own
  description. When an archive is added or renamed, update `public/index.html`
  and the list in `scripts/check-seo-image.sh` together.

Check locally:

```sh
docker build -f site/Dockerfile -t arkhiv-site:local site
bash scripts/check-seo-image.sh arkhiv-site:local
```

Merging to main deploys it; see `docs/runbooks/seo-ci-deployment.md`.
