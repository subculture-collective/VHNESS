# Arkhiv brand files

The masters live in SUBCULT Studio: `subculture-collective/subcult-tv`, `studio/brands/arkhiv/`, revision `159489c`. Read the pack's `GUIDE.md` and `alt-text.md` there before making new artwork. Do not edit these copies; change the Studio pack, rebuild it, and copy the new files here.

## Copied byte for byte

| File | Studio source | SHA-256 |
| --- | --- | --- |
| `mark.svg` | `logos/mark.svg` | `bb09af00162e1d23a59cced52584beb8fa1c84271751742041e684633c5f710c` |
| `lockup.svg` | `logos/lockup.svg` | `2478addf4b10c82329be579c4f83e3711760ecf43be863f2216da008e292afa3` |
| `og.png` | `product/og.png` (1200×630) | `22730e3549cd4df6327d2f6fe271f2dc0dea5161840bed0b5d1704791c47fc9a` |

## Derived from the pack

| File | Made from | SHA-256 |
| --- | --- | --- |
| `favicon.svg` | `mark.svg` with one `#f3efe4` background rectangle inserted after its title; the mark's own elements are unchanged. The ground keeps the dark ink visible in dark browser tabs | `d697ab73ee4f460ec3df5f56ac6ebcf61e8e720e696eaaf91b8270078cd9f085` |
| `favicon.ico` | `favicon.svg` rendered with `rsvg-convert` at 16, 32 and 48 px, joined with `magick` | `75f66339d61518b595654e09a37bc118d321e75427aefaf69ffcc4b143be5e96` |
| `apple-touch-icon.png` | `mark.svg` rendered at 180×180 on the pack background `#f3efe4` | `efd11464f200486a091cfc59ba206ef58bba43cea7ca6af2bd82cbad79bf66ed` |
| `readme-lockup.png` | `lockup.svg` rendered at 704×256 and centred on `#f3efe4` at 832×384, so the dark ink stays readable on dark README themes | `b25d0c05410ec15522324e755abab598f3ec15568f498fc46f2da170865c4b0c` |

## Interface theme

`web/src/styles.css` and `web/public/site.json` carry the pack's four colours: background `#f3efe4`, text `#1a1816`, accent `#d8262c` and secondary `#2b59c3` (the `label` token, used for small labels because the accent is below 4.5:1 on the background). The pack defines no surface, muted or border colour, so the interface derives three from its ground and ink: surface `#e7e1d1`, muted `#57524b` (6.74:1 on the background) and border `#857f72`. The pack has one light palette; the interface has no dark theme.

The header shows `mark.svg` at 56 px (44 px on phones; the pack's minimum is 32 px) beside the name set in Newsreader 700, which is the lockup's arrangement with live text.

| File in `web/public/fonts/` | Made from the pack's `fonts/` | SHA-256 |
| --- | --- | --- |
| `newsreader-700.woff2` | `Newsreader-1.ttf` | `d7d457a902ff4282a9d6bb1ae733ca676ea007908281170c0eaf4f7902e1f66f` |
| `public-sans-400.woff2` | `Public-Sans-0.ttf` | `9c46bdb4b08063a6918d987f1f94b42063b161b044afa469c3ef40d2812fbae2` |
| `public-sans-700.woff2` | `Public-Sans-1.ttf` | `0dcc244af9e305fa22800096bd52263427a076261999e9213ca8afd113225e4d` |
| `ibm-plex-mono-400.woff2` | `IBM-Plex-Mono-0.ttf` | `a31d39f4e9df8d421000b6b36c570f2ef7c80b69eb953c48225709f6ac84261e` |

Each was subset to Latin with `pyftsubset --flavor=woff2 --layout-features='*'` (fontTools) and the Unicode ranges Basic Latin, Latin-1 Supplement and common punctuation. The three `*-OFL.txt` licences beside them are byte-for-byte copies from the pack.

## Served copies

The public interface serves identical copies from `web/public/`: `mark.svg`, `favicon.svg`, `favicon.ico`, `apple-touch-icon.png` and `og.png`. `web/public/site.json` names `/og.png` as the link preview and carries its alt text. Replace both copies together.
