## 0.4.8

`usePersonal` and the chat hooks read `community` from `/api/version` once per
page. When the engine says accounts or chat are off, they skip the account
session request or stop polling the chat room, instead of requesting routes
that answer 404 (chat retried every minute). Engines that do not report
`community` (before core 0.4.9) are probed as before.

## 0.4.7

Catalog filters accept `label` (an accepted label ID) and `text` (archive-wide
search of indexed page text). Items may carry `labels` (accepted labels only) and
`text_hits` (up to three matching pages per item with a snippet).
`catalogLabelsURL()` builds the accepted-label listing URL. Needs core 0.4.6.

## 0.4.6

`representationURL` accepts a source page from a provider other than the Internet
Archive when the resolver names it in `source_provider`, the policy is
`local_preserve`, and the page is `https` on that provider's host or a subdomain.
Responses without `source_provider` keep the exact `archive.org/details` check, and
source streaming stays archive.org-only. `sourceProvider()` returns the provider
name for labels. Needs core 0.4.5 to receive `source_provider`.

## 0.4.5

Accept reviewed captions served beside processed local video (`/api/video-captions/`)
for `local_preserve` playback; source-streamed captions keep the exact archive.org
checks. `Compatibility` now follows the reference engine check: an engine bound to
one collection must match, an engine scoped by a collection list must include the
brand collection, and an unscoped engine is accepted. The audio player reports its
initial loading state on mount; that report was previously unreachable.

## 0.4.4

Fetch audio and video with anonymous CORS so browser volume extensions can route
reviewed cross-origin streams through Web Audio without silencing them. Custom
consumer players must set `crossOrigin="anonymous"` on their media elements too.
Remote media servers must permit the consumer origin through CORS.

## 0.4.3

Accept exact, reviewed nested audio source filenames in catalog URLs. Reject
absolute paths, traversal, backslashes and control characters. Print, video and
local media contracts retain their existing validation.

## 0.4.2

Show retryable archive errors when an upstream proxy returns HTML or unreadable
JSON. Video, print and audio catalog requests retain scoped missing-item and
structured source errors without exposing JSON parser details to viewers.

## 0.4.1

Warn when browser storage rejects favorite or pending-sync changes. Retain the
newer page copy across hook remounts instead of reading stale persisted values.
Export remains available, and a successful later write clears the warning.

## 0.4.0

Add opt-in audio replay discussion with separate media rooms, exact recording/representation context and measured playback state. Preserve existing video behavior and keep audio read/post switches and moderators independent. Personal restore upgrades additive module migrations before reapplying current deletion and scoped moderation evidence.

## 0.3.1

Apply volume and mute preferences to audio and native mixed-media players. Shared audio stations expose measured replay states and fresh resume/rejoin, with seeking retained for on-demand recordings. Add mixed-catalog creator/date filters, stable item links and canonical favorite references.

## 0.3.0

Add reviewed video artwork and metadata filters, persistent volume, explicit scheduled replay controls, and grid/table library composition. Add optional personal favorites and authenticated channel discussion hooks. Services remain disabled until configured by each consumer.

# Shared viewer changes

## Unreleased

- Add reviewed source artwork with safe placeholders and explicit series labels.
- Persist video volume and mute across player changes, with a memory fallback.
- Distinguish channel replay, joining, paused and on-demand playback; provide
  channel pause/resume and rejoin controls without viewer schedule seeking.
- Add safe source/selected-file details and full-catalog metadata filters.
- Keep branded interfaces and independent package adoption in consumer repositories.

## 0.2.3

- Let remote source seeks finish before shared-channel clock corrections.
- Correct drift only when the target position is buffered and playable.

## 0.2.2

- Request deadlines show a clear retry message while preserving caller cancellation.
- Catalog requests accept a bounded timeout for integrity-checked continuous audio resolution.

## 0.2.1

- Export shared audio controls for distinctive consumer interfaces; the neutral
  player uses the same seek bounds, transport state and completion behavior.
- Validate download transport URLs against an explicitly requested action;
  a read URL cannot become a download URL through client-side substitution.

## 0.2.0 (superseded before publication)

- Add the catalog entry for scoped mixed-media metadata browsing and validated
  representation transport URLs.
- Add the `@subcult/archive-viewer/print` entry with PDF page controls, zoom,
  readable text and optional search through a consumer-supplied PDF backend.
- Keep PDF runtime dependencies and worker assets in the consumer. Video
  consumers continue to import the existing root entry.
- Add the audio entry for full recordings, reviewed sets, track navigation,
  source credits and shared station clock calculations.
- Scope video channel/playback requests to the selected collection and hide
  stale sessions, errors and loading state during collection changes.

Print reading requires the engine's reviewed representation access. Search must
report actual indexed coverage; OCR without page mapping remains document text.

## 0.1.1

- Tie catalog results and errors to the active collection, query, year and page.
  A pending or failed search cannot display a previous search's episode cards.
- Ignore catalog responses after their request is aborted.
- Give a branded library the accessible name supplied by its heading, announce
  result counts and expose a busy state while results load.

Compatible with video API 1 and configuration contract 1. Consumers retain their
page layouts, assets, CSS and collection delivery policies. Upgrade the exact
package pin, lockfile, engine digest and release receipt together.

## 0.1.0

Initial package extraction: compiled ESM and declarations, native video player,
source-policy validation, headless viewing/schedule logic and composed screen,
library, compatibility and replay components. React remains a peer dependency.
