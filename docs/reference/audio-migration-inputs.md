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
preparation. The apply step below stores it in the core unchanged.

Original recording identities, concert identities, source and reviewed playback
rows, tracks, file variants, reviewed sets, warnings, source/taper/transferer
credits and legacy schedule stay retained. Metadata-only recordings get no
transport review. Existing published private playback gets a new playback
review. Download and acquisition stay disabled. Relay and scheduling are
enabled only for recordings Dedhed already relayed and for those that keep a
public approval with `--retain-public-approvals`, the only path to public
publication.

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

## Applying the plan

`scripts/apply_audio_migration.py` applies a prepared directory with the
`vhness` operator commands, in this order:

1. `migrate`. With `ARKHIV_READER_ROLE` set, it also grants the read-only role.
2. `collection`, with `local_preserve` policy and the `--budget` and
   `--reserve` you pass. Without them, the collection must already exist.
3. `retain-legacy` with `retained-export.json`. This keeps the complete legacy
   tables and fills the show index behind calendar, dates, on-this-day and
   shows.
4. `import-source` for each `source-*.json`, after checking its bytes against
   the plan.
5. `import-catalog` for each `item-*.json`, keeping legacy recording IDs as
   item and representation IDs.
6. `review-transport` for each `review-*.json`, with `--approve-public` only
   when the prepared review is public.
7. `register-audio` for each `audio-*.json`. The core checks every original
   track, the continuous file's hash and its measured duration, so set
   `VHNESS_MEDIA` to the originals directory and `VHNESS_DERIVATIVES` to the
   continuous files.
8. `schedule-audio` with `station-schedule.json`, which the script writes into
   the plan directory from `legacy_schedule`.

```sh
DATABASE_URL=... VHNESS_COLLECTION=dedhed \
VHNESS_MEDIA=/private/dedhed/originals VHNESS_DERIVATIVES=/private/dedhed/media \
python3 scripts/apply_audio_migration.py \
  --plan /private/audio-inputs --budget 2147483648 --reserve 1073741824
```

**Station timeline.** Dedhed and the core compute the station position the same
way: the current program is the one whose start and duration contain the clock,
and the offset is the time since its start. Each program has an absolute start,
so the script copies the revision and every program's start and duration
unchanged, naming the legacy recording ID as item and representation. For any
clock, the core's `/api/station` then returns the same recording, offset and
revision that Dedhed's backend returned. It does not run `renew-public-audio`,
which would start a new timeline at the current hour. When the legacy programs
run out, run `renew-public-audio` as Dedhed's operator ran `public-schedule`.
The receipt records when the legacy schedule ends and whether it had already
ended at apply time; an ended schedule leaves the station off the air until
renewal.

Before any command, the script stops if:

- `retained-export.json` is larger than 268,435,456 bytes (256 MiB), the most
  `retain-legacy` reads;
- the legacy schedule plays a recording listed in `not_schedule_eligible`, or
  one without a schedule review or audio input;
- a program is longer than its prepared audio, programs overlap, there are more
  than 1,000 programs, or the revision is not a valid core identity;
- `VHNESS_MEDIA` or `VHNESS_DERIVATIVES` is unset;
- `station-schedule.json` already exists with other content.

Before each step, the script reads the database with `psql` and skips the step
when the database already holds that exact input. Rerunning a completed plan
therefore changes nothing, and rerunning after a failure resumes at the failed
step. A step whose current state differs from the input stops the run instead of
overwriting it: a changed catalog item, review or audio receipt, another
retained audio export, or a station schedule stored after the legacy one, such
as a renewal. After each command, the script checks that the database now holds
the input.

The first failure stops the run with the step number, command and target. Each
run writes a new receipt (`apply-receipt-<UTC time>.json` in the plan directory,
or `--receipt`, mode 0600, never overwritten) with the plan hash, each step's
input file, input hash and outcome, the station summary and any failure.
Connection handling and `--vhness`/`--psql` prefixes work as in
[print migration](print-migration-inputs.md#applying-the-plan).

`scripts/test_apply_audio_migration.py` covers ordering, reruns, failures, the
pre-checks and the station position at fixed clocks with a fake runner. With
`ARKHIV_TEST_DATABASE_URL` (an empty, disposable database), `VHNESS_BIN` and
`ffprobe`, it also applies a synthetic checkpoint with three recordings twice
through the real commands, checks that the second run leaves every table
unchanged and that legacy IDs are kept, and compares the public compatibility
`/api/station` with the legacy schedule at the server's reported time.

## Verification scope

The required repository gate covers corrupted and missing originals,
checkpoint dump and continuous-file mismatches, the recorded schedule,
unreviewed recordings, differing source observations and action separation.
Actual private browser decoding, independent common-platform restore, audio
fidelity and public release are separate evidence.
