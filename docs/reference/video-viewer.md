# Video viewer contracts

This additive source contract supports Arkhiv issues #106 through #109. The
headless HTTP API remains the integration boundary. React components and hooks
are optional; frontend markup and deployment belong to each consumer.

## Safe episode metadata

`GET /api/episodes` lists published episodes within the configured/requested
collection scope. Public mode also requires the current episode publication
review. Search and filters run before stable ordering and 24-item pagination.

`GET /api/version` includes `community: {personal, chat}` (core 0.4.9 and
later), true when the optional community module serves accounts and chat. The
SDK skips the account and chat routes when they are reported off.

`serve` reads listing summaries only for its configured collections and reuses
them for 30 seconds, starting the first load at startup. Until that first load
finishes, a listing waits up to 10 seconds for it. An expired copy (up to
five minutes old) is returned while one background refresh runs, so a listing
can show a review change up to about 30 seconds late. Single-episode, playback
and channel requests still read the database on every request, so a withdrawn
episode cannot play while it remains in a cached listing.

Listings and channel responses add optional `subjects`, `languages`, `artwork`
and `video` fields. The `video` projection carries the original source title,
`title_origin`, source page, snapshot hash and available selected-file format,
duration, dimensions and byte size. Credits, rights and license URL remain
existing fields. Missing values remain missing, and original date precision is
unchanged. Editorial title overrides are labeled separately from source titles.

Subjects and languages are read from the episode's pinned complete snapshot,
so existing catalogs need no reimport or metadata refetch. Only strings and
string arrays from those named fields are accepted. Compound source strings
remain intact. Unknown/private source fields are never added to the projection.

In public mode, `GET /api/episodes/{id}` returns the safe `episode` projection,
policy, source hash and unavailability state. It omits editorial decision notes,
local paths and the complete raw file inventory. Private operator detail retains
its existing inventory/decision contract. No stored fields are deleted or
rewritten to make the viewer response smaller.

## Filters

All filters combine with AND. Existing `collection`, `q`, `year` and `page`
parameters remain supported.

| Parameter | Meaning |
| --- | --- |
| `date_from`, `date_to` | Inclusive YYYY-MM-DD bounds |
| `subject`, `language`, `creator` | Case-insensitive exact match against available source values |
| `duration_min`, `duration_max` | Inclusive finite nonnegative seconds for the selected eligible video file |

Unknown dates/durations do not match a requested bound. Partial dates represent
uncertain intervals; the entire year/month must fit within the requested date
range. A 2026 date matches a complete 2026 range but not a single October day.
The existing year filter still matches known dates of that year. Reversed or
malformed ranges, nonfinite/negative durations, invalid years and text filters
over 500 bytes return 400. Missing subjects/languages/creators do not match their
corresponding nonempty filters.

`videoCatalogURL` encodes the shared query contract. `useViewer` exposes `filters`
and `setFilters`, which resets pagination. `Library` enables the additional
reference controls with `metadataFilters`; consumers can provide their own UI.

## Reviewed artwork

An operator may add `artwork` to an episode review, alongside its existing
playback decision:

```json
{
  "file": "__ia_thumb.jpg",
  "snapshot_sha256": "<exact current snapshot SHA-256>"
}
```

The referenced nonprivate, nonempty JPEG, PNG or WebP must occur in the retained
file inventory. Nested source thumbnail paths are supported with individually
escaped path segments; empty, dot, parent, NUL and backslash segments are
rejected. SVG is excluded. A source refresh invalidates the artwork reference
until the review pins the new snapshot. Public episode scope/publication gates
still apply. Review does not download images or video into application storage.

For file-based episodes sharing a parent source item, this defaults to `series`
artwork. An operator can explicitly review an episode image by also supplying
`episode_file` equal to that episode's exact source video file. This is an
editorial association, not an inferred mapping from a shared parent title or date.
The optional client validates source identity, path and snapshot reference before
requesting an image. `EpisodeArtwork` renders a text fallback after image failure
and labels shared series artwork. `Player` supports the same reviewed poster.

Read-only metadata inspection on 2026-10-05 found Bill Nye parent title-screen,
item-tile and nested per-video thumbnail entries. Its current public frontend
rendered SCIENCE text placeholders, with only the header logo as an image and
no active video. The sampled Democracy Now! item also supplied item-tile and
nested thumbnail entries. No video payload was requested during this inventory.
Source metadata responses and browser screenshots are outside Git.

## Preferences and replay controls

`useVideoPreferences` and the optional `Player` retain validated volume/mute
preferences under `arkhiv.video.preferences.v1`, per browser origin. Invalid or
blocked storage never prevents playback. A memory fallback retains preferences
across remounts within the current page. Stored preferences do not bypass browser
autoplay restrictions.

On-demand playback keeps native seeking controls. Channel playback provides
pause, resume/rejoin, volume, mute, captions, fullscreen and read-only time,
without a scrub bar or native seeking shortcuts. Resume/rejoin requests fresh
channel state. `Screen` supplies `onRejoin` through `useViewer.tune`. Custom
consumers using `Player` should also supply `onRejoin`; the legacy `onPaused`
callback fallback rejoins a paused channel when backed by `useViewer`.

`VideoState` distinguishes loading, buffering, blocked, paused, playing,
joining, synchronized, unavailable and ended. `useViewer` exposes the reported
`playbackState` and `onPlaybackState`; custom players can supply those reports.
Only a playing, ready, non-seeking picture within three seconds of the
schedule receives the synchronized label. This matches the current drift
correction tolerance and does not claim frame-accurate broadcast.

Labels distinguish scheduled archival replay from on-demand and keep original
dates visible. Pausing does not stop the shared clock. Watching from the
beginning enters on-demand, and Join channel remains nearby. Gaps, exhaustion,
source failures and autoplay denial are explicit. No viewer action writes a
shared schedule.

## Adoption

These changes are source work awaiting a new qualified package/core release.
They do not modify the published 0.2.3 package bytes or existing core images.
Adopt released contracts through each consumer's own reviewed pins and PR.
HTTP-only consumers can use the metadata/filter contract and implement their
own playback state and controls. Bill Nye, Democracy Now! and the template
retain separate browser and deployment evidence in their consumer issues.
