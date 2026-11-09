# Lineage popularity coverage and refresh bounds

Decision for [issue 175](https://git.subcult.tv/subculture-collective/arkhiv/issues/175).
Observed November 9, 2026, from Kvant. This is a bounded provider investigation,
not popularity ingestion, scheduling, acquisition, or full-catalog qualification.

## Observed coverage

The private Lineage catalog returned 837 visible recordings for November 9 across
years. A newest-first page supplied 20 distinct artists with recordings from
2024–2025. Two additional local-catalog queries selected four different artists
from each of the 1960–1999 and 2000–2019 bands. Their sampled recordings were
from 1999 and 2019 respectively. The bands contained 101 and 616 recordings.
This covers four recording years and 28 artists, not a random or age-complete
corpus sample.

Two bounded requests to the documented Views API returned HTTP 200:

| Request | Real recording IDs | Missing-ID control | Response bytes | Elapsed |
|---|---:|---:|---:|---:|
| Recent anniversary artists | 20 | 0 | 1,843 | 639 ms |
| Older/middle-year artists | 8 | 1 | 830 | 513 ms |

All 28 real IDs returned `have_data: true` and integer `all_time`, `last_30day`,
and `last_7day` fields. The older/middle-year sample ranged from 507 to 12,547
all-time views, zero to 20 30-day views, and zero to one 7-day view. Real zero
recent counts therefore must not be treated as absent data.

The deliberately nonexistent control returned all three counts as zero with
`have_data: false`. Missing data must be represented separately from observed
zero. No credentials were sent to the public metrics provider. The investigation
made two provider requests, requested 29 IDs total, acquired no media, and did not
exercise a rate limit or retry against the live provider.

The [official beta API](https://archive.org/developers/views_api.html) documents
batched identifiers and these four fields. It supplies view counts, not verified
completed listens. Documentation and this small successful sample do not establish
a provider quota, uptime guarantee, or whole-corpus coverage.

## Refresh decision

Use the short summary endpoint, not detailed per-day/geolocation data. Retain
provider identity, item identifier, exact bounded response bytes, observation time,
`have_data`, and count windows independently of editorial metadata. Use the
30-day and all-time rankings for the initial popularity blend. Retain 7-day counts
without inventing a separate trending product surface.

Start with the following conservative **application limits**, not claimed provider
limits:

- At most 50 IDs per request, 1 MiB per response, and a 15-second request timeout.
- At least one second between provider requests; honor a longer `Retry-After`.
- At most 20 requests, 1,000 candidate IDs, or two minutes per operator run,
  whichever bound is reached first.
- Refresh today's anniversary and the next two days, using local visible catalog
  candidates. Persist the day/identifier cursor and per-item observation state.
- Treat snapshots up to 24 hours old as fresh for ranking. Preserve the last
  usable snapshot with its timestamp during failures; missing/stale entries can
  remain discovery candidates without a fresh-popularity claim.

The local November calendar counted 837, 881, and 853 recordings for November
9–11: 2,571 candidates before any playback-eligibility selection. The proposed
1,000-item run cap would require at least three runs for that window if every
candidate were refreshed. Do not fetch the entire imported catalog daily, and
do not assume a single run completes the window. Provider budgets and cadence
must be read back after actual implementation; this task installs no timer.

Import and refresh are separate outcomes: first implement a validated snapshot
import with a real operator caller, then the bounded provider refresh/resume path.
Serving home/search/calendar requests must only read retained data. A provider
outage must not block rendering or overwrite valid metrics with synthetic zeros.

## Validation and failure cases for the implementation

The adjacent [synthetic fixtures](../research/lineage-views-fixtures.json) are
invented test inputs, not retained provider responses. They cover the important
distinctions for the importer/client work:

- Observed zero versus `have_data: false` versus an omitted identifier.
- Negative, fractional, string, missing, or out-of-range counters.
- A successful partial batch which retains earlier snapshots for omitted IDs.
- HTTP 429 with a longer cooldown, timeout, and malformed JSON.

Do not deliberately trigger throttling in live validation. Test those conditions
with a fake HTTP provider, and validate nonnegative integral counters within the
chosen database integer range before import. Keep raw evidence even when a
response cannot replace a usable metrics projection.

## Reproduction and evidence limits

1. Select bounded, visible anniversary IDs from the private source-catalog API,
   keeping one per artist and recording the year strata.
2. Request `/views/v1/short/ID1,ID2,...` from `be-api.us.archive.org` with an
   identifying User-Agent, timeout, and response-size bound.
3. Compare requested/returned IDs, `have_data`, field types and response bytes.
   Keep exact responses and ID lists outside Git.
4. Re-run failure fixtures against the future import/client implementation before
   claiming retry, cooldown, or resumable behavior.

Private local evidence is retained under
`~/.local/state/lineage-wave1/`: the bounded ID samples, exact responses, aggregate
summaries, and observation context. This report intentionally commits no response
snapshots or raw catalog records. Wider recording ages, other calendar days,
provider failure behavior, refresh scheduling, and sustained coverage remain
separate qualification work for the implementation issues.
