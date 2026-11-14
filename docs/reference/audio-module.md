# Shared audio module

The GPL-3.0-only music model and player contracts come from Dedhed revision
`e5843b17fd33d2e09c88fe3e5022153a0c9e8adc`. Recording identities, concert grouping,
ordered tracks, source variants, taper/transfer credits and uncertain set warnings
remain distinct from print editions and video episodes. Reviewed set ranges are
required for set playback; retained uncertain boundaries grant no controls.

Non-concert recordings use their reviewed kind, such as `spoken-word` or
`field-recording`, and may omit concert grouping. A single-file recording has one
track at offset zero; it needs no inferred set. Unsupported original formats
remain in the retained source inventory. Generic audio transport supports MP3,
FLAC, Ogg, WAV and M4A, subject to browser codec support; continuous delivery needs
a separately verified local derivative and never substitutes one silently.

The `@subcult/archive-viewer/audio` entry supplies continuous audio controls,
track navigation, volume, source credit, bounded on-demand sets and a station
clock helper. The consumer owns page layout and navigation. A
local seek or pause changes only that listener. Rejoining requests the current
server program. The `0.2.0` source version remains unreleased.

`useAudioStation` supplies sequential, abortable status polling and explicit
join/rejoin/leave commands. It hides a previous collection's state immediately
and keeps polling updates separate from the listener's current playback session.
Consumers can use that hook with `AudioPlayer` or compose their own controls.

Continuous audio is a derivative, not an Internet Archive original. Operator
`register-audio` accepts an already prepared file in `VHNESS_DERIVATIVES` with a
receipt binding its snapshot, complete input manifest digest, bytes, SHA-256,
duration, tool and independent recovery digest. It verifies every preserved input
and the derivative, then probes duration through an inherited file descriptor
with time, memory and output limits. It neither acquires nor transcodes media.
Originals and this derivative together must fit the collection's reviewed budget.
A receipt digest alone does not prove that recovery occurred.

Playback resolves through current per-track reviews and exact preserved hashes.
Corrupt or revoked inputs cannot expose their continuous derivative, including
Range and conditional responses. Filesystem paths are absent from viewer JSON.
No generic download or public relay permission follows from playback.

The server hashes each original track and continuous derivative once, then
trusts it while its device, inode, size, modification time and change time stay
the same. Playback, Range reads and station polls, native and compatibility
routes alike, open the file and compare that identity instead of rereading it.
A file whose identity changed is hashed again before it is served, so changed
bytes are still refused, even when the modification time was put back. A file
changed within the last second is hashed on every request until it is quiet,
because kernel timestamps are too coarse to tell two quick writes apart. Each
file is rehashed at least every 24 hours to catch corruption that leaves its
metadata alone, and the state is per process, so a restart hashes again.
Concurrent requests for a file that is not yet verified wait for one read.
Rights and review checks still run on every request. `register-audio` and
`renew-public-audio` still hash every file in full.

`schedule-audio` appends a collection-specific immutable station revision. Every
program, including future programs, requires independent scheduling and playback
reviews. The station endpoint checks all current policy bindings before returning
its current program, and verifies the current program's bytes as described above. Exhausted schedules
and withdrawn future eligibility return an unavailable state. Existing video
schedules keep their original schema and identity.

Disposable tests use generated PCM tones to check duration, source/derivative
integrity, Range access, collection boundaries, server clock offsets, revoked
future programs and independent on-demand access. Real music, browser playback,
continuous fidelity, brand migration, independent restore and hosted publication
require separate evidence.
