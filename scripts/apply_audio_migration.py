#!/usr/bin/env python3
"""Apply prepared audio migration inputs with the vhness operator commands.

Walks the directory written by prepare_audio_migration.py in order: migrate,
collection, retain-legacy, import-source, import-catalog, review-transport,
register-audio and schedule-audio. The last step stores the legacy station
schedule unchanged, so the station keeps its timeline. A step whose exact input
the database already holds is skipped, so a rerun changes nothing. The first
failure stops the run. Every run writes a receipt.
"""
import argparse
import calendar
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shlex
import subprocess

from apply_print_migration import (ApplyError, PsqlState, catalog_matches, digest, libpq_env, record, run_steps,
                                   sha)

RETAIN_LIMIT = 256 << 20  # retain-legacy reads at most this many bytes
SCHEDULE_FILE = 'station-schedule.json'
VALID_ID = re.compile(r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,200}')
INSTANT = re.compile(r'(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)')


def identity(value):
    if not isinstance(value, str) or not VALID_ID.fullmatch(value):
        raise ApplyError('Invalid identity in plan: %r' % (value,))
    return value


def instant(value):
    """Nanoseconds since the epoch for an RFC 3339 time, as Go's time.Time holds it."""
    match = INSTANT.fullmatch(value) if isinstance(value, str) else None
    if not match:
        raise ApplyError('Invalid station program start: %r' % (value,))
    seconds = calendar.timegm(tuple(int(part) for part in match.groups()[:6]))
    zone = match.group(8)
    if zone != 'Z':
        offset = int(zone[1:3]) * 3600 + int(zone[4:6]) * 60
        seconds -= offset if zone[0] == '+' else -offset
    return seconds * 10**9 + int((match.group(7) or '').ljust(9, '0'))


def program_end(start, duration):
    return start + int(duration * 1e9)  # Go: p.Start.Add(time.Duration(p.Duration * float64(time.Second)))


def core_schedule(legacy, collection, reviews, assets):
    """Convert the plan's legacy_schedule into schedule-audio input with the same programs and revision.

    Dedhed and the core compute the station position the same way, from each program's absolute start
    and duration, so copying them keeps the recording and offset for every clock."""
    revision, programs = legacy.get('revision'), legacy.get('programs')
    if not isinstance(revision, str) or not VALID_ID.fullmatch(revision):
        raise ApplyError('Legacy station revision is not a valid core identity: %r' % (revision,))
    if not isinstance(programs, list) or not 1 <= len(programs) <= 1000:
        raise ApplyError('Legacy station schedule needs 1 to 1000 programs; the core stores no more')
    if legacy.get('not_schedule_eligible'):
        raise ApplyError('Legacy station schedule plays recordings without a schedule review: '
                         + ', '.join(legacy['not_schedule_eligible']))
    converted, end = [], None
    for program in programs:
        recording, start, duration = identity(program.get('recording_id')), program.get('start'), program.get('duration')
        review = reviews.get(recording, {}).get('review', {})
        if not (review.get('schedule') and review.get('playback')) or recording not in assets:
            raise ApplyError('Legacy station schedule plays a recording without schedule review or audio input: ' + recording)
        if isinstance(duration, bool) or not isinstance(duration, (int, float)) or not 0 < duration <= 86400:
            raise ApplyError('Invalid legacy station program duration for ' + recording)
        if duration > assets[recording]['duration']:
            raise ApplyError('Legacy station program is longer than the prepared audio: ' + recording)
        begins = instant(start)
        if end is not None and begins < end:
            raise ApplyError('Legacy station programs overlap at ' + start)
        end = program_end(begins, duration)
        converted.append({'item_id': recording, 'representation_id': recording, 'start': start, 'duration': duration})
    return {'collection': collection, 'revision': revision, 'programs': converted}, end


def normalized(schedule):
    return (schedule.get('collection'), schedule.get('revision'),
            [(p.get('item_id'), p.get('representation_id'), instant(p.get('start')), float(p.get('duration')))
             for p in schedule.get('programs') or []])


def plan_steps(directory, notes, budget=None, reserve=None):
    """Return the ordered operator steps and the converted station schedule for a prepared directory."""
    plan_file = directory / 'migration-plan.json'
    plan = json.loads(plan_file.read_text())
    collection = identity(plan['collection'])

    def read(name):
        path = directory / name
        if not path.is_file() or path.is_symlink():
            raise ApplyError('Plan input missing: ' + name)
        return path, path.read_bytes()

    def named(paths, prefix):
        found = {}
        for path in paths:
            found[identity(path.name[len(prefix):-len('.json')])] = path
        return found

    retained = directory / 'retained-export.json'
    if retained.is_file() and retained.stat().st_size > RETAIN_LIMIT:
        raise ApplyError('retained-export.json is %d bytes; retain-legacy reads at most %d (256 MiB)'
                         % (retained.stat().st_size, RETAIN_LIMIT))
    steps = [{'command': 'migrate', 'args': []},
             {'command': 'collection', 'collection': collection, 'budget': budget, 'reserve': reserve,
              'args': ['--collection', collection, '--policy', 'local_preserve',
                       '--budget', str(budget), '--reserve', str(reserve)]}]
    path, raw = read('retained-export.json')
    steps.append({'command': 'retain-legacy', 'collection': collection, 'file': path, 'sha256': digest(raw),
                  'args': ['--collection', collection, '--file', str(path), '--notes', notes]})
    for source in plan['sources']:
        path, raw = read('source-' + sha(source['sha256']) + '.json')
        if digest(raw) != source['sha256']:
            raise ApplyError('Source input differs from the plan: ' + path.name)
        steps.append({'command': 'import-source', 'item': identity(source['item_id']), 'file': path, 'sha256': source['sha256'],
                      'args': ['--id', source['item_id'], '--collection', collection, '--file', str(path)]})
    items = named(sorted(directory.glob('item-*.json')), 'item-')
    if len(items) != plan['recordings']:
        raise ApplyError('Item inputs differ from the plan recording count')
    for recording, path in items.items():
        raw = path.read_bytes()
        item = json.loads(raw)
        if item['id'] != recording:
            raise ApplyError('Item input name differs from its identity: ' + path.name)
        steps.append({'command': 'import-catalog', 'item': recording, 'input': item, 'file': path,
                      'sha256': digest(raw), 'args': ['--file', str(path), '--notes', notes]})
    review_files = named(sorted(directory.glob('review-*.json')), 'review-')
    audio_files = named(sorted(directory.glob('audio-*.json')), 'audio-')
    if (len(review_files) != plan['private_playback_recordings'] or set(audio_files) != set(review_files)
            or not set(review_files) <= set(items)):
        raise ApplyError('Review and audio inputs differ from the plan playback recordings')
    reviews, assets = {}, {}
    for recording, path in review_files.items():
        raw = path.read_bytes()
        reviews[recording] = data = json.loads(raw)
        args = ['--id', recording, '--representation', recording, '--collection', collection, '--file', str(path)]
        if data['review']['public']:
            args.append('--approve-public')
        steps.append({'command': 'review-transport', 'item': recording, 'representation': recording,
                      'collection': collection, 'file': path, 'sha256': digest(raw), 'input': data, 'args': args})
    if sum(1 for data in reviews.values() if data['review']['public']) != plan['public_playback_recordings']:
        raise ApplyError('Public reviews differ from the plan public playback count')
    for recording, path in audio_files.items():
        raw = path.read_bytes()
        assets[recording] = json.loads(raw)
        steps.append({'command': 'register-audio', 'item': recording, 'representation': recording,
                      'collection': collection, 'file': path, 'sha256': digest(raw), 'input': assets[recording],
                      'args': ['--id', recording, '--representation', recording, '--collection', collection,
                               '--file', str(path), '--notes', notes]})
    station = None
    if plan.get('legacy_schedule'):
        schedule, ends = core_schedule(plan['legacy_schedule'], collection, reviews, assets)
        data = (json.dumps(schedule, indent=2) + '\n').encode()
        path = directory / SCHEDULE_FILE
        steps.append({'command': 'schedule-audio', 'collection': collection, 'file': path, 'sha256': digest(data),
                      'input': schedule, 'data': data,
                      'args': ['--collection', collection, '--file', str(path), '--notes', notes]})
        station = {'revision': schedule['revision'], 'programs': len(schedule['programs']), 'ends_at_ns': ends}
    return plan_file, steps, station


def current_state(state, step):
    """Compare the database with a step's input: 'missing', 'present' or a difference message."""
    command = step['command']
    if command == 'collection':
        found = state.collection(step['collection'])
        if found is None:
            return 'missing'
        if found['policy'] != 'local_preserve':
            return 'collection exists without local preservation'
        if step['budget'] is not None and (found['budget'], found['reserve']) != (step['budget'], step['reserve']):
            return 'collection budget or reserve differs; review it with review-budget'
        return 'present'
    if command == 'retain-legacy':
        found = state.retained_audio_export(step['collection'])
        if found is None:
            return 'missing'
        return 'present' if found == step['sha256'] else 'another retained audio export exists; review a later recovery import explicitly'
    if command == 'import-source':
        found = state.snapshot(step['sha256'])
        if found is None:
            return 'missing'
        return 'present' if found == step['item'] else 'source snapshot belongs to another item'
    if command == 'import-catalog':
        found = state.catalog_item(step['item'])
        if found is None:
            return 'missing'
        return 'present' if catalog_matches(found, step['input']) else 'catalog item differs from the prepared item; review it explicitly'
    if command == 'review-transport':
        found = state.transport_review(step['item'], step['representation'], step['collection'])
        if found is None:
            return 'missing'
        return 'present' if found == step['input'] else 'current transport review differs; review it explicitly'
    if command == 'register-audio':
        found = state.audio_asset(step['item'], step['representation'], step['collection'])
        if found is None:
            return 'missing'
        return 'present' if found == step['input'] else 'registered audio receipt differs; review it explicitly'
    if command == 'schedule-audio':
        found = state.audio_schedule(step['collection'])
        if found is None:
            return 'missing'
        try:
            same = normalized(found) == normalized(step['input'])
        except (ApplyError, TypeError, ValueError):
            same = False
        return 'present' if same else 'current station schedule differs from the legacy schedule; review it explicitly'
    raise ApplyError('Unknown step ' + command)


def write_schedule(step):
    """Write the converted schedule for schedule-audio, keeping an identical earlier copy."""
    path = step['file']
    if path.is_symlink() or (path.exists() and path.read_bytes() != step['data']):
        raise ApplyError(SCHEDULE_FILE + ' differs from the converted legacy schedule; move it aside to convert again')
    if not path.exists():
        with open(path, 'xb') as handle:
            handle.write(step['data'])
        os.chmod(path, 0o400)


def iso(nanoseconds):
    return datetime.fromtimestamp(nanoseconds // 10**9, timezone.utc).isoformat().replace('+00:00', 'Z')


def apply(directory, run, state, receipt_path, notes=None, budget=None, reserve=None, environ=os.environ, now=None, log=print,
          clock=None):
    """Run every missing step in order. `run(argv)` returns an exit code; `state` reads the database."""
    now = now or (lambda: datetime.now(timezone.utc).isoformat(timespec='seconds'))
    clock = clock or (lambda: datetime.now(timezone.utc).timestamp())
    plan_file = directory / 'migration-plan.json'
    plan_sha = digest(plan_file.read_bytes())
    receipt = {'plan': str(plan_file), 'plan_sha256': plan_sha, 'started_at': now(), 'status': 'failed', 'steps': []}

    def work():
        _, steps, station = plan_steps(directory, notes or 'Applied audio migration plan ' + plan_sha, budget, reserve)
        if any(s['command'] == 'register-audio' for s in steps):
            unset = [name for name in ('VHNESS_MEDIA', 'VHNESS_DERIVATIVES') if not environ.get(name)]
            if unset:
                raise ApplyError('Set ' + ' and '.join(unset) + ' to the original and continuous audio roots before registering audio')
        if station:
            receipt['station'] = {'revision': station['revision'], 'programs': station['programs'],
                                  'ends_at': iso(station['ends_at_ns']),
                                  'ended_before_apply': station['ends_at_ns'] <= clock() * 1e9}
            if receipt['station']['ended_before_apply']:
                log('note: the legacy station schedule ended at', receipt['station']['ends_at'],
                    '; the station stays off the air until renew-public-audio')
            write_schedule(steps[-1])
        run_steps(steps, run, lambda step: current_state(state, step), receipt, log)
    return record(receipt, receipt_path, work, now)


class AudioPsqlState(PsqlState):
    """Read-only audio checks through psql, beside the shared catalog and review checks."""

    def retained_audio_export(self, collection):
        return self.query("SELECT to_json(sha256) FROM retained_legacy_exports WHERE collection_id=:'c' AND module='audio' ORDER BY created_at DESC LIMIT 1;", c=collection)

    def audio_asset(self, item, representation, collection):
        return self.query("SELECT receipt FROM audio_assets WHERE item_id=:'i' AND representation_id=:'r' AND collection_id=:'c';", i=item, r=representation, c=collection)

    def audio_schedule(self, collection):
        return self.query("SELECT schedule FROM audio_schedules WHERE collection_id=:'c' ORDER BY created_at DESC,revision DESC LIMIT 1;", c=collection)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', required=True, type=Path, help='Directory written by prepare_audio_migration.py')
    parser.add_argument('--receipt', type=Path, help='New receipt file (default: apply-receipt-<UTC time>.json in the plan directory)')
    parser.add_argument('--vhness', default='vhness', help='Operator command prefix, for example "docker compose exec -T api vhness"')
    parser.add_argument('--psql', default='psql', help='Read-only psql command prefix for state checks')
    parser.add_argument('--notes', help='Review notes recorded by retain-legacy, import-catalog, register-audio and schedule-audio')
    parser.add_argument('--budget', type=int, help='Local byte budget when the collection does not exist yet')
    parser.add_argument('--reserve', type=int, help='Free-space reserve when the collection does not exist yet')
    args = parser.parse_args()
    if (args.budget is None) != (args.reserve is None):
        parser.error('--budget and --reserve go together')
    receipt = args.receipt or args.plan / ('apply-receipt-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '.json')
    try:
        env = libpq_env(os.environ)
    except (ApplyError, OSError) as error:
        raise SystemExit('Stopped before any command: ' + str(error))
    vhness = shlex.split(args.vhness)
    result = apply(args.plan, lambda argv: subprocess.run(vhness + argv).returncode,
                   AudioPsqlState(shlex.split(args.psql), env), receipt, args.notes, args.budget, args.reserve)
    print('Receipt:', receipt)
    if result['status'] != 'complete':
        raise SystemExit('Stopped: ' + result['failure'])
    print('Applied', result['counts']['applied'], 'steps;', result['counts']['unchanged'], 'already present')


if __name__ == '__main__':
    main()
