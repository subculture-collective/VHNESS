# Shared archive viewer

`@subcult/archive-viewer` provides the typed video API, playback/schedule model,
viewer hook and composed player/library/channel components. This is an optional React client package for the headless Arkhiv HTTP core.
Frontends in any framework can use the API directly without this package. React
is supplied only by consumers choosing these components. The package includes no brand assets, global CSS or page layout.

```tsx
import { useViewer, Screen, ChannelPanel, Library } from '@subcult/archive-viewer';

function Archive() {
  const viewer = useViewer('democracy_now_vid');
  return <main>
    <Screen viewer={viewer}><p>Select an episode.</p></Screen>
    <ChannelPanel viewer={viewer} />
    <Library viewer={viewer} />
  </main>;
}
```

Consumers own page composition and CSS for the documented classes already used
by the video apps. `Player` uses native on-demand controls and channel-specific
pause/resume, rejoin, volume and fullscreen controls, source attribution,
caption controls and explicit loading/failure messages. A keyed player owns one
asset; channel clock updates do not reload it. `useViewer` exposes listener-local
state and commands independently of page layout. Set
`useViewer(collection, { scrollTarget: false })` for consumer-owned navigation,
or supply a custom selector. The default retains the existing `#screen` scroll
behavior and honors reduced motion.

The unreleased viewer changes add reviewed artwork, safe video details, metadata
filters, volume/mute persistence and explicit channel playback state. Consumers
using `Player` directly should pass `onRejoin={viewer.tune}` and
`onPlaybackState={viewer.onPlaybackState}`. `Screen` supplies both. See the
[video viewer contract](../../docs/reference/video-viewer.md) for API fields,
filter semantics, state labels and independent adoption requirements.

The root entry supports video API version `1`. `Compatibility` verifies that the
engine serves the brand collection: an engine bound to one collection must match
it, an engine scoped by a collection list must include it, and an unscoped engine
is accepted. Video channel/playback requests name the
selected collection; typed print/audio entries have their own contracts. Consumers
own mixed media page composition. Acquisition/publication policies stay on the
server.

The root entry also exports the video sites' viewer tools: `VideoTools`,
`useTheater`, `episodeLink`, `useViewerLinks`, `AccountPanel`, `FavoriteAction`,
`FavoriteShelf`, `ChatPanel` and `useLibraryLayout`. They use the same class
names as the video apps (`viewer-tools`, `account-panel`, `favorite-shelf`,
`chat-panel` and others); pass the product name to `AccountPanel`.

Build with the pinned root pnpm version using `pnpm build:packages`. Packages
ship compiled ESM and declarations; consumers need no sibling source checkout.
The legacy `scripts/export_viewer.py` remains a compatibility path until every
consumer adopts released packages. Edit these sources; web compatibility barrels
and legacy exported copies are not separate editing authorities.

GPL-3.0-only software. Media and identity assets retain their separate owners.

The print entry, introduced in the unreleased `0.2.0` source version and
published in `0.2.1`, exports `PrintReader` from
`@subcult/archive-viewer/print`. Consumers supply its PDF backend and worker
assets; importing the root entry does not load a PDF runtime. See the engine's
[print module contract](../../docs/reference/print-module.md) for indexing and
access requirements.

The audio entry exports `AudioPlayer`, reviewed set bounds and station clock
helpers from `@subcult/archive-viewer/audio`. The engine supplies a verified
continuous recording URL; the consumer owns station polling and navigation.
Source credits, warnings and track order remain in the recording contract.

The catalog entry exports `useCatalog`, `useCatalogCreators` (creator
suggestions served by the core), scoped metadata query types and reviewed
representation URL validation from `@subcult/archive-viewer/catalog`. Consumers
own collection filters, item detail, navigation and media composition. The hook
hides superseded results immediately and cancels their requests. The neutral
reference frontend composes all four entries; a single-media brand can import
only the entries it uses.
# Distinct audio interfaces

`useAudioControls` from `@subcult/archive-viewer/audio` supplies bounded seeking,
play/pause state, volume, teardown and completion callbacks for consumer-owned
markup. Supply the audio element ref and reviewed start/end positions.

Set `crossOrigin="anonymous"` on custom audio and video elements before assigning
their source. Volume extensions route media through Web Audio, which silences
cross-origin responses fetched without CORS. Remote media servers must allow the
consumer origin; same-origin engine media retains its normal session behavior.
Shared `AudioPlayer` and `Player` components set this attribute themselves.

Source resolution, access decisions, schedule lookup, metadata and visual layout remain
the consumer's responsibility. `AudioPlayer` uses the same hook.

Optional personal favorites and channel discussion hooks are exported from the root entry. Consumers own the interface and same-origin session service. See [community configuration](../../docs/reference/community.md) for the trust, database and recovery contract.
