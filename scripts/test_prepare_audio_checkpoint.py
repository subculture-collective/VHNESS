import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from prepare_audio_migration import prepare_checkpoint


def sha(data, name='sha256'):
    return hashlib.new(name, data).hexdigest()


def recording(identity, tracks):
    return {'id': identity, 'concert_id': 'concert-' + identity, 'title': 'Title ' + identity, 'artist': 'Artist',
            'date': '2000-01-01', 'sets': [], 'duration': 2.0 * len(tracks),
            'tracks': [{'title': 'Song ' + str(index), 'position': index, 'start': 2.0 * index, 'duration': 2.0,
                        'file': {'name': name, 'format': 'VBR MP3', 'bytes': len(data), 'md5': sha(data, 'md5')}}
                       for index, (name, data) in enumerate(tracks)]}


class PublicCheckpointTests(unittest.TestCase):
    """Synthetic public checkpoint: one public and one private-playback recording."""

    def fixture(self, root):
        self.root = root
        checkpoint = root / 'checkpoints/20260101T000000Z'
        checkpoint.mkdir(parents=True)
        media, originals = root / 'media', root / 'originals'
        media.mkdir()
        originals.mkdir()
        dump = b'synthetic public database dump'
        (checkpoint / 'database.dump').write_bytes(dump)
        continuous = b'synthetic continuous audio'
        continuous_name = sha(continuous) + '.m4a'
        (media / continuous_name).write_bytes(continuous)
        self.tracks = [('one.mp3', b'first synthetic track'), ('two.mp3', b'second synthetic track!')]
        for _, data in self.tracks:
            (originals / (sha(data) + '.mp3')).write_bytes(data)
        (originals / 'unrelated.mp3').write_bytes(b'x' * len(self.tracks[0][1]))
        manifest = {'observed_at': '2026-01-01T00:00:00+00:00', 'revision': 'abc1234', 'images': {},
                    'dump_sha256': sha(dump), 'dump_bytes': len(dump),
                    'media': [{'name': continuous_name, 'sha256': sha(continuous), 'bytes': len(continuous)}], 'restore_qualified': False}
        (checkpoint / 'manifest.json').write_text(json.dumps(manifest))
        rows, snapshots = [], []
        for identity, tracks, media_name, media_hash in [
                ('public-show', self.tracks, continuous_name, sha(continuous)),
                ('private-show', [('private.mp3', b'private synthetic track')], 'private.m4a', '1' * 64)]:
            played = recording(identity, tracks)
            raw = {'metadata': {'identifier': identity},
                   'files': [{'name': name, 'size': str(len(data)), 'md5': sha(data, 'md5'), 'sha1': sha(data, 'sha1')} for name, data in tracks]}
            source_hash = sha(json.dumps(raw).encode())
            snapshots.append({'sha256': source_hash, 'item_id': identity, 'raw': raw})
            rows.append({'id': identity, 'source_hash': source_hash, 'source': played, 'playback': played,
                         'review': {'published': True, 'playback': True, 'kind': 'audience', 'notes': 'Reviewed.'},
                         'media': media_name, 'media_sha256': media_hash})
        public = rows[0]
        schedule = {'revision': 'legacy-revision', 'programs': [
            {'recording_id': 'public-show', 'start': '2026-01-01T00:00:00Z', 'duration': 4.0},
            {'recording_id': 'private-show', 'start': '2026-01-01T00:00:04Z', 'duration': 2.0}]}
        data = {'recordings': rows, 'source_snapshots': snapshots,
                'station_schedule': [{'id': 1, 'schedule': schedule, 'updated_at': '2026-01-01T00:00:00Z'}],
                'source_items': [], 'public_approval_history': [],
                'public_approvals': [{'recording_id': 'public-show', 'approved': True, 'source_hash': public['source_hash'],
                                      'media_sha256': public['media_sha256'], 'policy_basis': 'Synthetic policy', 'reviewer': 'Tester'}]}
        self.export, self.receipt = root / 'export.json', root / 'receipt.json'
        self.write(data, {'independent_host': 'host-a', 'restored_on': 'host-b', 'dump_sha256': sha(dump)})
        return [checkpoint, media, originals, self.export, self.receipt, root / 'output', 'audio-pilot']

    def write(self, data, receipt=None):
        self.export.write_text(json.dumps(data))
        receipt = receipt or json.loads(self.receipt.read_text())
        receipt['counts'] = {table: len(rows) for table, rows in data.items()}
        self.receipt.write_text(json.dumps(receipt))

    def output(self, args, name):
        return json.loads((args[5] / name).read_text())

    def test_plan_from_public_checkpoint_and_retained_originals(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            plan = prepare_checkpoint(*args, retain_public=True)
            self.assertEqual(plan['donor']['kind'], 'public-checkpoint')
            self.assertEqual(plan['donor']['checkpoint'], '20260101T000000Z')
            self.assertEqual(plan['public_playback_recordings'], 1)
            self.assertEqual(plan['playback_without_checkpoint_media'], ['private-show'])
            self.assertEqual({source['origin'] for source in plan['sources']}, {'restored-database-json'})
            self.assertIsNone(plan['snapshot_mappings'][0]['retained_file_sha256'])
            review = self.output(args, 'review-public-show.json')
            self.assertTrue(review['review']['public'])
            self.assertEqual(review['preservation']['manifest'], [
                {'file': name, 'bytes': len(data), 'sha256': sha(data), 'path': sha(data) + '.mp3'} for name, data in self.tracks])
            audio = self.output(args, 'audio-public-show.json')
            self.assertIn('20260101T000000Z', audio['tool'])
            self.assertFalse((args[5] / 'review-private-show.json').exists())
            self.assertTrue((args[5] / 'item-private-show.json').exists())

    def test_plan_records_legacy_station_schedule(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            schedule = prepare_checkpoint(*args, retain_public=True)['legacy_schedule']
            self.assertEqual(schedule['revision'], 'legacy-revision')
            self.assertEqual(schedule['updated_at'], '2026-01-01T00:00:00Z')
            self.assertEqual([program['recording_id'] for program in schedule['programs']], ['public-show', 'private-show'])
            self.assertEqual(schedule['programs'][0]['start'], '2026-01-01T00:00:00Z')
            self.assertEqual(schedule['not_schedule_eligible'], ['private-show'])
            self.assertEqual(self.output(args, 'migration-plan.json')['legacy_schedule'], schedule)

    def test_schedule_naming_unknown_recording_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            data = json.loads(self.export.read_text())
            data['station_schedule'][0]['schedule']['programs'][0]['recording_id'] = 'missing-show'
            self.write(data)
            with self.assertRaisesRegex(ValueError, 'unknown recording'):
                prepare_checkpoint(*args)
            self.assertFalse(args[5].exists())

    def test_malformed_schedule_program_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            data = json.loads(self.export.read_text())
            del data['station_schedule'][0]['schedule']['programs'][1]['recording_id']
            self.write(data)
            with self.assertRaisesRegex(ValueError, 'Invalid legacy station schedule'):
                prepare_checkpoint(*args)

    def test_original_with_other_bytes_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            name, data = self.tracks[1]
            (args[2] / (sha(data) + '.mp3')).write_bytes(b'X' + data[1:])
            with self.assertRaisesRegex(ValueError, 'No single retained original matches.*two.mp3'):
                prepare_checkpoint(*args)
            self.assertFalse(args[5].exists())

    def test_missing_original_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            (args[2] / (sha(self.tracks[0][1]) + '.mp3')).unlink()
            with self.assertRaisesRegex(ValueError, 'No single retained original matches.*one.mp3'):
                prepare_checkpoint(*args)

    def test_snapshot_checksum_is_also_checked(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            data = json.loads(self.export.read_text())
            data['source_snapshots'][0]['raw']['files'][0]['sha1'] = '0' * 40
            self.write(data)
            with self.assertRaisesRegex(ValueError, 'No single retained original matches.*one.mp3'):
                prepare_checkpoint(*args)

    def test_dump_without_track_checksum_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            data = json.loads(self.export.read_text())
            del data['recordings'][0]['playback']['tracks'][0]['file']['md5']
            self.write(data)
            with self.assertRaisesRegex(ValueError, 'records no size and checksum'):
                prepare_checkpoint(*args)

    def test_symlink_in_original_directory_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            (args[2] / 'link.mp3').symlink_to(args[2] / (sha(self.tracks[0][1]) + '.mp3'))
            with self.assertRaisesRegex(ValueError, 'only regular files'):
                prepare_checkpoint(*args)

    def test_dump_differing_from_checkpoint_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            (args[0] / 'database.dump').write_bytes(b'synthetic public database dumX')
            with self.assertRaisesRegex(ValueError, 'checkpoint manifests differ'):
                prepare_checkpoint(*args)

    def test_corrupt_continuous_file_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            continuous = next(args[1].iterdir())
            continuous.write_bytes(b'X' + continuous.read_bytes()[1:])
            with self.assertRaisesRegex(ValueError, 'continuous file differs'):
                prepare_checkpoint(*args)

    def test_public_recording_missing_from_checkpoint_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            data = json.loads(self.export.read_text())
            private = data['recordings'][1]
            data['public_approvals'].append({'recording_id': 'private-show', 'approved': True, 'source_hash': private['source_hash'],
                                             'media_sha256': private['media_sha256'], 'policy_basis': 'Synthetic', 'reviewer': 'Tester'})
            self.write(data)
            with self.assertRaisesRegex(ValueError, 'lacks the continuous file of a public recording: private-show'):
                prepare_checkpoint(*args, retain_public=True)


if __name__ == '__main__':
    unittest.main()
