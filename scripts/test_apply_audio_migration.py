import copy
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.request
import wave

import apply_audio_migration
from apply_audio_migration import AudioPsqlState, apply, instant, program_end
from apply_print_migration import libpq_env
from prepare_audio_migration import prepare_checkpoint

COLLECTION = 'fixture-dedhed'
SHOW_A, SHOW_B, SHOW_C = 'fixture-show-a', 'fixture-show-b', 'fixture-show-c'  # C has no playback
BASE = 1794614400 * 10**9  # 2026-11-14T00:00:00Z


def sha(data, name='sha256'):
    return hashlib.new(name, data).hexdigest()


def go_time(nanoseconds):
    """Format nanoseconds since the epoch as Go's time.Time JSON in UTC."""
    seconds, fraction = divmod(nanoseconds, 10**9)
    text = time.strftime('%Y-%m-%dT%H:%M:%S', time.gmtime(seconds))
    return text + (('.%09d' % fraction).rstrip('0') if fraction else '') + 'Z'


def wav(seconds):
    """A silent 8 kHz mono WAV of exactly `seconds`."""
    path = tempfile.NamedTemporaryFile(suffix='.wav', delete=False).name
    with wave.open(path, 'wb') as out:
        out.setnchannels(1)
        out.setsampwidth(1)
        out.setframerate(8000)
        out.writeframes(b'\x80' * int(8000 * seconds))
    data = Path(path).read_bytes()
    os.unlink(path)
    return data


def legacy_programs(start, count):
    """Dedhed's public-schedule shape: the selected recordings repeated back to back."""
    programs, durations = [], [(SHOW_A, 4.0), (SHOW_B, 3.0)]
    for index in range(count):
        recording, duration = durations[index % 2]
        programs.append({'recording_id': recording, 'start': go_time(start), 'duration': duration})
        start = program_end(start, duration)
    return programs


def station_at(programs, now, key='recording_id'):
    """Dedhed's and the core's Schedule.At: the program whose [start, start+duration) holds now, and the offset."""
    for program in programs:
        offset = (now - instant(program['start'])) / 1e9
        if 0 <= offset < program['duration']:
            return program[key], offset
    return None


def build_fixture(root, programs):
    """Write a synthetic Dedhed public checkpoint and prepare its inputs. Returns (inputs, originals, media)."""
    checkpoint, media, originals = root / 'checkpoints/20261114T000000Z', root / 'media', root / 'originals'
    for path in (checkpoint, media, originals):
        path.mkdir(parents=True)
    dump = b'synthetic public database dump'
    (checkpoint / 'database.dump').write_bytes(dump)
    rows, snapshots, items, approvals, entries = [], [], [], [], []
    for recording, tracks, playable in ((SHOW_A, ['a1.mp3', 'a2.mp3'], True), (SHOW_B, ['b1.mp3'], True),
                                        (SHOW_C, ['c1.mp3'], False)):
        files = [(name, ('synthetic original ' + recording + ' ' + name).encode()) for name in tracks]
        for _, data in files:
            (originals / (sha(data) + '.mp3')).write_bytes(data)
        played = {'id': recording, 'concert_id': 'concert-' + recording, 'artist': 'Fixture Band', 'date': '1977-05-08',
                  'venue': 'Fixture Hall', 'location': 'Ithaca, NY', 'title': 'Fixture ' + recording, 'kind': 'audience',
                  'source': 'AUD > cassette', 'taper': 'Taper', 'transferer': 'Transferer', 'sets': [], 'warnings': [],
                  'synthetic': False, 'duration': 2.0 * len(files),
                  'tracks': [{'title': 'Song ' + str(i), 'position': i, 'start': 2.0 * i, 'duration': 2.0, 'variants': [],
                              'file': {'name': name, 'format': 'VBR MP3', 'bytes': len(data), 'md5': sha(data, 'md5')}}
                             for i, (name, data) in enumerate(files)]}
        if recording == SHOW_B:  # one longer track, so the two programs differ in length
            played['tracks'][0].update(duration=3.0)
            played['duration'] = 3.0
        raw = {'metadata': {'identifier': recording, 'title': played['title']},
               'files': [{'name': n, 'size': str(len(d)), 'md5': sha(d, 'md5'), 'sha1': sha(d, 'sha1')} for n, d in files]}
        source_hash = sha(json.dumps(raw).encode())
        snapshots.append({'sha256': source_hash, 'item_id': recording, 'raw': raw})
        row = {'id': recording, 'source_hash': source_hash, 'source': played, 'playback': None, 'media': None,
               'media_sha256': None, 'review': {'published': playable, 'playback': playable, 'kind': 'audience',
                                                'notes': 'Synthetic reviewed decision.'}}
        if playable:
            continuous = wav(played['duration'])
            name = sha(continuous) + '.wav'
            (media / name).write_bytes(continuous)
            entries.append({'name': name, 'sha256': sha(continuous), 'bytes': len(continuous)})
            row.update(playback=played, media=name, media_sha256=sha(continuous))
            approvals.append({'recording_id': recording, 'approved': True, 'source_hash': source_hash,
                              'media_sha256': row['media_sha256'], 'policy_basis': 'Synthetic policy', 'reviewer': 'Tester'})
        rows.append(row)
        items.append({'id': recording, 'show_date': '1977-05-08', 'date_text': '1977-05-08', 'artist': 'Fixture Band',
                      'venue': 'Fixture Hall', 'location': 'Ithaca, NY', 'title': played['title'], 'kind': 'audience',
                      'source': 'AUD', 'lineage': '', 'taper': 'Taper', 'transferer': 'Transferer', 'downloads': 1,
                      'avg_rating': 0, 'num_reviews': 0, 'item_size': 1, 'added_at': None, 'tracks': [], 'songs': '',
                      'duration': played['duration'], 'warnings': [], 'summary': {}, 'snapshot_sha256': source_hash,
                      'observed_at': '2026-11-14T00:00:00Z', 'updated_at': '2026-11-14T00:00:00Z'})
    (checkpoint / 'manifest.json').write_text(json.dumps({'observed_at': '2026-11-14T00:00:00+00:00', 'revision': 'abc1234',
                                                          'dump_sha256': sha(dump), 'dump_bytes': len(dump), 'media': entries}))
    schedule = {'revision': 'public-20261114T000000Z', 'programs': programs}
    data = {'recordings': rows, 'source_snapshots': snapshots, 'source_items': items, 'public_approvals': approvals,
            'public_approval_history': [], 'station_schedule': [{'id': 1, 'schedule': schedule, 'updated_at': '2026-11-14T00:00:00Z'}]}
    (root / 'export.json').write_text(json.dumps(data))
    (root / 'receipt.json').write_text(json.dumps({'independent_host': 'host-a', 'restored_on': 'host-b', 'dump_sha256': sha(dump),
                                                   'counts': {table: len(rows) for table, rows in data.items()}}))
    prepare_checkpoint(checkpoint, media, originals, root / 'export.json', root / 'receipt.json', root / 'inputs', COLLECTION,
                       retain_public=True)
    return root / 'inputs', originals, media


class FakeDatabase:
    """Applies operator commands to in-memory state and answers the same checks as AudioPsqlState."""

    def __init__(self):
        self.calls, self.fail, self.tables = [], None, {
            'collections': {}, 'retained': [], 'snapshots': {}, 'items': {}, 'reviews': {}, 'assets': {}, 'schedules': []}

    def run(self, argv):
        self.calls.append(argv)
        command, flags, rest = argv[0], {}, argv[1:]
        while rest:
            if rest[0] == '--approve-public':
                flags['public'], rest = True, rest[1:]
            else:
                flags[rest[0][2:]], rest = rest[1], rest[2:]
        if command == self.fail:
            return 1
        t = self.tables
        data = Path(flags['file']).read_bytes() if 'file' in flags else b''
        if command == 'collection':
            t['collections'][flags['collection']] = {'policy': flags['policy'], 'budget': int(flags['budget']), 'reserve': int(flags['reserve'])}
        elif command == 'retain-legacy':
            t['retained'].append(hashlib.sha256(data).hexdigest())
        elif command == 'import-source':
            t['snapshots'][hashlib.sha256(data).hexdigest()] = flags['id']
        elif command == 'import-catalog':
            item = json.loads(data)
            if item['id'] in t['items']:
                return 1  # duplicate primary key
            t['items'][item['id']] = {
                'title': item['title'], 'description': item['description'], 'date': item['date'], 'creator': item['creator'],
                'editorial': item['editorial'], 'collections': sorted(item['collections']),
                'representations': [{'id': r['id'], 'media': r['media'], 'source_item_id': r['source_item_id'],
                                     'snapshot_sha256': r['snapshot_sha256'], 'file': r['file'],
                                     'collections': sorted(r['collections']), 'extension': copy.deepcopy(r['extension'])}
                                    for r in item['representations']]}
        elif command == 'review-transport':
            review = json.loads(data)
            if review['review']['public'] != flags.get('public', False):
                return 1
            t['reviews'][(flags['id'], flags['representation'], flags['collection'])] = review
        elif command == 'register-audio':
            if (flags['id'], flags['representation'], flags['collection']) not in t['reviews']:
                return 1
            t['assets'][(flags['id'], flags['representation'], flags['collection'])] = json.loads(data)
        elif command == 'schedule-audio':
            schedule = json.loads(data)
            if any(s['revision'] == schedule['revision'] for s in t['schedules']):
                return 1  # primary key (collection, revision)
            t['schedules'].append(schedule)
        return 0

    def collection(self, collection):
        return self.tables['collections'].get(collection)

    def retained_audio_export(self, collection):
        return self.tables['retained'][-1] if self.tables['retained'] else None

    def snapshot(self, sha256):
        return self.tables['snapshots'].get(sha256)

    def catalog_item(self, item):
        return self.tables['items'].get(item)

    def transport_review(self, item, representation, collection):
        return self.tables['reviews'].get((item, representation, collection))

    def audio_asset(self, item, representation, collection):
        return self.tables['assets'].get((item, representation, collection))

    def audio_schedule(self, collection):
        return self.tables['schedules'][-1] if self.tables['schedules'] else None


class ApplyAudioMigrationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.programs = legacy_programs(BASE, 6)
        self.inputs, originals, media = build_fixture(self.root, self.programs)
        self.env = {'VHNESS_MEDIA': str(originals), 'VHNESS_DERIVATIVES': str(media)}
        self.db = FakeDatabase()
        self.receipts = 0

    def apply(self, **options):
        self.receipts += 1
        options.setdefault('budget', 1 << 20)
        options.setdefault('reserve', 1)
        options.setdefault('clock', lambda: BASE / 1e9)
        receipt = self.root / ('receipt-%d.json' % self.receipts)
        result = apply(self.inputs, self.db.run, self.db, receipt, environ=self.env, log=lambda *_: None, **options)
        self.assertEqual(json.loads(receipt.read_text()), result)
        return result

    def commands(self):
        return [call[0] for call in self.db.calls]

    def plan(self):
        return json.loads((self.inputs / 'migration-plan.json').read_text())

    def edit_plan(self, change):
        path = self.inputs / 'migration-plan.json'
        plan = self.plan()
        change(plan)
        path.chmod(0o600)
        path.write_text(json.dumps(plan))

    def test_applies_every_input_in_order_with_legacy_identities(self):
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'complete', receipt.get('failure'))
        self.assertEqual(self.commands(), ['migrate', 'collection', 'retain-legacy'] + ['import-source'] * 3
                         + ['import-catalog'] * 3 + ['review-transport'] * 2 + ['register-audio'] * 2 + ['schedule-audio'])
        self.assertEqual(sorted(self.db.tables['items']), [SHOW_A, SHOW_B, SHOW_C])
        for recording, item in self.db.tables['items'].items():
            self.assertEqual([r['id'] for r in item['representations']], [recording])
            self.assertEqual(item['representations'][0]['extension']['audio']['recording_id'], recording)
        self.assertEqual(sorted(k[0] for k in self.db.tables['assets']), [SHOW_A, SHOW_B])
        self.assertTrue(all('--approve-public' in c for c in self.db.calls if c[0] == 'review-transport'))
        self.assertEqual(receipt['counts'], {'applied': 13, 'unchanged': 0, 'ran': 1, 'failed': 0})
        self.assertEqual(receipt['station'], {'revision': 'public-20261114T000000Z', 'programs': 6,
                                              'ends_at': '2026-11-14T00:00:21Z', 'ended_before_apply': False})
        self.assertEqual(oct((self.root / 'receipt-1.json').stat().st_mode & 0o777), '0o600')

    def test_schedule_keeps_the_legacy_timeline(self):
        self.apply()
        stored = self.db.tables['schedules'][-1]
        self.assertEqual(stored['revision'], 'public-20261114T000000Z')
        self.assertEqual(stored['collection'], COLLECTION)
        for clock in (BASE - 1, BASE, BASE + 1_500_000_000, BASE + 4 * 10**9, BASE + 9_999_999_999, BASE + 21 * 10**9):
            self.assertEqual(station_at(stored['programs'], clock, 'item_id'), station_at(self.programs, clock), clock)
        self.assertEqual(station_at(stored['programs'], BASE + 5_250_000_000, 'item_id'), (SHOW_B, 1.25))

    def test_second_run_changes_nothing(self):
        self.apply()
        before = copy.deepcopy(self.db.tables)
        self.db.calls.clear()
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'complete', receipt.get('failure'))
        self.assertEqual(self.commands(), ['migrate'])
        self.assertEqual(self.db.tables, before)
        self.assertEqual(receipt['counts'], {'applied': 0, 'unchanged': 13, 'ran': 1, 'failed': 0})

    def test_stops_at_first_failure_and_resumes(self):
        self.db.fail = 'register-audio'
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('register-audio', receipt['failure'])
        self.assertEqual(receipt['steps'][-1]['outcome'], 'failed')
        self.assertNotIn('schedule-audio', self.commands())
        self.db.fail, self.db.calls = None, []
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'complete', receipt.get('failure'))
        self.assertEqual(self.commands(), ['migrate', 'register-audio', 'register-audio', 'schedule-audio'])

    def test_newer_station_schedule_is_not_overwritten(self):
        self.apply()
        self.db.tables['schedules'].append({'collection': COLLECTION, 'revision': 'public-renewed', 'programs': []})
        self.db.calls.clear()
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('station schedule differs', receipt['failure'])
        self.assertNotIn('schedule-audio', self.commands())

    def test_unreviewed_scheduled_recording_stops_before_any_command(self):
        self.edit_plan(lambda plan: plan['legacy_schedule'].update(not_schedule_eligible=[SHOW_B]))
        receipt = self.apply()
        self.assertIn('without a schedule review: ' + SHOW_B, receipt['failure'])
        self.assertEqual(self.db.calls, [])

    def test_program_longer_than_its_audio_stops_before_any_command(self):
        self.edit_plan(lambda plan: plan['legacy_schedule']['programs'][1].update(duration=3.5))
        receipt = self.apply()
        self.assertIn('longer than the prepared audio: ' + SHOW_B, receipt['failure'])
        self.assertEqual(self.db.calls, [])

    def test_oversized_retained_export_stops_before_any_command(self):
        size = (self.inputs / 'retained-export.json').stat().st_size
        original, apply_audio_migration.RETAIN_LIMIT = apply_audio_migration.RETAIN_LIMIT, size - 1
        self.addCleanup(setattr, apply_audio_migration, 'RETAIN_LIMIT', original)
        receipt = self.apply()
        self.assertEqual(receipt['failure'], 'retained-export.json is %d bytes; retain-legacy reads at most %d (256 MiB)' % (size, size - 1))
        self.assertEqual(self.db.calls, [])

    def test_audio_registration_requires_both_roots(self):
        self.env = {'VHNESS_MEDIA': self.env['VHNESS_MEDIA']}
        receipt = self.apply()
        self.assertIn('VHNESS_DERIVATIVES', receipt['failure'])
        self.assertEqual(self.db.calls, [])

    def test_changed_schedule_file_is_not_reused(self):
        (self.inputs / 'station-schedule.json').write_text('{}')
        receipt = self.apply()
        self.assertIn('station-schedule.json differs', receipt['failure'])
        self.assertEqual(self.db.calls, [])

    def test_ended_legacy_schedule_is_noted(self):
        receipt = self.apply(clock=lambda: BASE / 1e9 + 3600)
        self.assertEqual(receipt['status'], 'complete')
        self.assertTrue(receipt['station']['ended_before_apply'])

    def test_plan_without_schedule_skips_the_schedule_step(self):
        self.edit_plan(lambda plan: plan.update(legacy_schedule=None))
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'complete', receipt.get('failure'))
        self.assertNotIn('schedule-audio', self.commands())
        self.assertNotIn('station', receipt)

    def test_receipt_is_never_overwritten(self):
        receipt = self.root / 'receipt.json'
        receipt.write_text('kept')
        with self.assertRaises(FileExistsError):
            apply(self.inputs, self.db.run, self.db, receipt, budget=1, reserve=1, environ=self.env, log=lambda *_: None)
        self.assertEqual(receipt.read_text(), 'kept')

    def test_instant_reads_go_times(self):
        self.assertEqual(instant('2026-11-14T00:00:00Z'), BASE)
        self.assertEqual(instant('2026-11-14T00:00:01.000000005Z'), BASE + 10**9 + 5)
        self.assertEqual(instant('2026-11-14T01:00:00+01:00'), BASE)
        self.assertEqual(go_time(BASE + 2_500_000_000), '2026-11-14T00:00:02.5Z')


@unittest.skipUnless(os.environ.get('ARKHIV_TEST_DATABASE_URL') and os.environ.get('VHNESS_BIN') and shutil.which('ffprobe'),
                     'set ARKHIV_TEST_DATABASE_URL (an empty disposable database) and VHNESS_BIN, and install ffprobe')
class FixtureDatabaseTests(unittest.TestCase):
    """Applies the synthetic fixture to a real disposable database through the real commands."""

    def test_fixture_migration(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        now = time.time_ns() // 10**9 * 10**9
        programs = legacy_programs(now - 60 * 10**9, 600)  # about 35 minutes around the test
        inputs, originals, media = build_fixture(root, programs)
        env = dict(os.environ, DATABASE_URL=os.environ['ARKHIV_TEST_DATABASE_URL'], VHNESS_COLLECTION=COLLECTION,
                   VHNESS_MEDIA=str(originals), VHNESS_DERIVATIVES=str(media),
                   VHNESS_MIGRATIONS=str(Path(__file__).resolve().parent.parent / 'migrations'))
        for inherited in ('ARKHIV_PUBLIC', 'DATABASE_URL_FILE'):  # the file would win over the disposable URL
            env.pop(inherited, None)
        vhness = os.environ['VHNESS_BIN']
        state = AudioPsqlState(['psql'], libpq_env(env))

        def run(argv):
            return subprocess.run([vhness] + argv, env=env).returncode

        def fingerprint():
            tables = state.query("SELECT json_agg(tablename ORDER BY tablename) FROM pg_tables WHERE schemaname='public';")
            return {t: state.query('SELECT json_build_object(\'rows\',count(*),\'md5\',md5(COALESCE(string_agg(x::text,\',\' ORDER BY x::text),\'\'))) FROM "%s" x;' % t) for t in tables}

        first = apply(inputs, run, state, root / 'first.json', budget=1 << 30, reserve=1, environ=env)
        self.assertEqual(first['status'], 'complete', first.get('failure'))
        before = fingerprint()
        second = apply(inputs, run, state, root / 'second.json', budget=1 << 30, reserve=1, environ=env)
        self.assertEqual(second['status'], 'complete', second.get('failure'))
        self.assertEqual(second['counts']['applied'], 0)
        self.assertEqual(fingerprint(), before)
        for recording in (SHOW_A, SHOW_B, SHOW_C):
            self.assertEqual([r['id'] for r in state.catalog_item(recording)['representations']], [recording])

        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        server = subprocess.Popen([vhness, 'serve'], env=dict(env, ARKHIV_COMPATIBILITY='audio', ARKHIV_PUBLIC='1',
                                                              API_ADDR='127.0.0.1:%d' % port))
        self.addCleanup(server.wait)
        self.addCleanup(server.terminate)

        def get(path):
            for _ in range(100):
                try:
                    with urllib.request.urlopen('http://127.0.0.1:%d%s' % (port, path), timeout=5) as response:
                        return json.loads(response.read())
                except OSError:
                    time.sleep(0.1)
            raise AssertionError('API unavailable: ' + path)

        self.assertEqual(get('/api/recordings/' + SHOW_A)['recording']['id'], SHOW_A)
        for _ in range(3):
            station = get('/api/station')
            clock = instant(station['server_time'])
            recording, offset = station_at(programs, clock)  # what Dedhed's backend plays at that instant
            self.assertEqual(station['program']['recording_id'], recording)
            self.assertEqual(station['revision'], 'public-20261114T000000Z')
            self.assertAlmostEqual(station['offset'], offset, places=6)
            time.sleep(1.3)


if __name__ == '__main__':
    unittest.main()
