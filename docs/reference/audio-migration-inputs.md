# Private audio migration inputs

`scripts/prepare_audio_migration.py` prepares catalog, representation review and
continuous-audio receipts from an independently restored legacy music pilot.
It does not acquire media, change originals, import a database or activate a
schedule. It verifies every retained backup checksum and complete exported table
counts before allocating an exclusive output directory.

The script accepts one of two donor layouts.

**Private backup.** Pass `--backup` with the backup directory. It contains
`manifest.json`, `catalog.dump`, and retained `data/sources`, `data/originals`,
`data/media` and acquisition receipts. The restore receipt records independent
and recovery hosts, verified file count, dump checksum and every table count.

**Public checkpoint.** Pass `--checkpoint` with one checkpoint directory written
by Dedhed's `scripts/public-runtime.py backup` (`database.dump` and
`manifest.json`), `--media` with the directory holding the continuous files the
checkpoint manifest lists, and `--originals` with a directory of the retained
original track files, copied from the private backup's `data/originals`. The
restore receipt records independent and recovery hosts, the dump checksum and
every table count. The script checks the dump's size and hash and each
continuous file against the checkpoint manifest. The checkpoint has no
acquisition receipts, so each track's original is found by the byte count and
MD5 the restored playback row records, and the size, MD5 and SHA-1 the restored
source snapshot lists for that file name. Exactly one file in the directory must
match; a missing, changed or ambiguous original stops preparation. The directory
may hold only regular files, and the preservation manifest names each original
by its file name there, so pass the same directory as the originals root when
registering audio.

```sh
python3 scripts/prepare_audio_migration.py \
  --checkpoint /private/dedhed/checkpoints/20261114T000000Z \
  --media /private/dedhed/media --originals /private/dedhed/originals \
  --export /private/restored-tables.json --receipt /private/restore-receipt.json \
  --output /private/audio-inputs --collection dedhed --retain-public-approvals
```

In both layouts, pass the complete legacy table export, a new output directory
and the collection ID.

A public checkpoint has no retained source files, so every catalog source is the
restored database observation serialized as new migration bytes, and the plan's
retained-file fields are `null`. It holds only the continuous files of public
recordings. A recording with private playback whose continuous file is absent
gets catalog metadata but no transport inputs, and the plan lists it under
`playback_without_checkpoint_media`. A recording with a current public approval
whose continuous file is absent stops preparation. Its audio receipt names the
checkpoint as the tool, because the checkpoint does not record the extraction
tool.

`migration-plan.json` names the donor under `donor` and records Dedhed's station
timeline under `legacy_schedule`: its revision, update time, every program
(`recording_id`, `start`, `duration`) unchanged, the recordings it plays, and
those without a schedule review (`not_schedule_eligible`). It is `null` when the
donor has no schedule, and a program naming a recording outside the export stops
preparation. Converting it into `schedule-audio` input belongs to the apply step.

Original recording identities, concert identities, source and reviewed playback
rows, tracks, file variants, reviewed sets, warnings, source/taper/transferer
credits and legacy schedule stay retained. Metadata-only recordings get no
transport review. Existing published private playback gets a new playback-only
review. Download, acquisition, relay, scheduling and public publication are
separate actions and remain disabled in this migration.

A retained source file may differ from its restored database observation. Both
JSON responses are included with separate hashes. The catalog binds to the
restored observation; a database-only observation is explicitly serialized as
new migration bytes. The plan retains the historical database hash, the retained
file hash, the new catalog hash and their equality result. It never describes
new serialization as preserved original network bytes.

Each continuous derivative binds the independently verified selected original
tracks, ordered manifest checksum, retained derivative checksum and duration,
recovery receipt and original extraction tool. `register-audio` must still
verify every input, current rights, byte counts and the actual derivative's
bounded duration probe. Original and derivative mounts must be separate roots.
The existing app, dump and restored legacy database own historical decisions
and schedules until a separately reviewed cutover.

The required repository gate covers corrupted and missing originals,
checkpoint dump and continuous-file mismatches, the recorded schedule,
unreviewed recordings, differing source observations and action separation.
Actual private browser decoding, independent common-platform restore, audio
fidelity and public release are separate evidence.
