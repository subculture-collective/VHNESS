import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from prepare_audio_migration import prepare


class AudioMigrationTests(unittest.TestCase):
    def fixture(self, root, approved=True, different=False):
        backup = root / 'backup'
        (backup / 'data/sources').mkdir(parents=True)
        (backup / 'data/originals').mkdir()
        (backup / 'data/media').mkdir()
        (backup / 'catalog.dump').write_bytes(b'retained database dump')
        original = b'original'
        (backup / 'data/originals/song.mp3').write_bytes(original)
        (backup / 'data/media/continuous.m4a').write_bytes(b'continuous')
        source = {'metadata': {'identifier': 'recording'}, 'files': [{'name': 'song.mp3', 'size': str(len(original))}], 'unknown': 'retained'}
        (backup / 'data/sources/recording.json').write_text(json.dumps(source))
        snapshot = dict(source, unknown='database observation') if different else source
        source_hash = hashlib.sha256(json.dumps(snapshot).encode()).hexdigest()
        recording = {'id': 'recording', 'concert_id': 'concert', 'title': 'Title', 'artist': 'Artist', 'date': '2000-01-01',
                     'sets': [], 'duration': 2, 'tracks': [{'title': 'Song', 'file': {'name': 'song.mp3', 'bytes': len(original)}}]}
        row = {'id': 'recording', 'source_hash': source_hash, 'source': recording,
               'review': {'published': approved, 'playback': approved, 'notes': 'Private review',
                          'title': 'Reviewed title', 'sets': [{'title': 'Reviewed set', 'first': 0, 'last': 0, 'reviewed': True}]},
               'playback': recording if approved else None, 'media': 'continuous.m4a',
               'media_sha256': hashlib.sha256(b'continuous').hexdigest()}
        acquisition = {'ffmpeg_version': 'retained version', 'files': [{'source_name': 'song.mp3', 'bytes': len(original),
                        'sha256': hashlib.sha256(original).hexdigest(), 'path': '/legacy/originals/song.mp3'}]}
        (backup / 'data/recording.acquisition.json').write_text(json.dumps(acquisition))
        manifest = {str(p.relative_to(backup)): hashlib.sha256(p.read_bytes()).hexdigest() for p in backup.rglob('*') if p.is_file()}
        (backup / 'manifest.json').write_text(json.dumps(manifest))
        data = {'recordings': [row], 'source_snapshots': [{'sha256': source_hash, 'item_id': 'recording', 'raw': snapshot}], 'station_schedule': []}
        export = root / 'export.json'; export.write_text(json.dumps(data))
        receipt = root / 'receipt.json'; receipt.write_text(json.dumps({'independent_host': 'nuc', 'restored_on': 'kuznya',
            'files_verified': len(manifest), 'dump_sha256': manifest['catalog.dump'], 'counts': {key: len(value) for key, value in data.items()}}))
        return backup, export, receipt, root / 'output', 'private-pilot'

    def test_reviewed_playback_does_not_grant_other_actions(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            plan = prepare(*args)
            review = json.loads((args[3] / 'review-recording.json').read_text())['review']
            self.assertEqual(plan['private_playback_recordings'], 1)
            self.assertTrue(review['playback'])
            self.assertEqual(plan['donor']['kind'], 'private-backup')
            self.assertIsNone(plan['legacy_schedule'])
            item = json.loads((args[3] / 'item-recording.json').read_text())
            audio = item['representations'][0]['extension']['audio']
            self.assertEqual(item['title'], 'Reviewed title')
            self.assertEqual(audio['recording']['sets'][0]['title'], 'Reviewed set')
            self.assertEqual(audio['tracks'][0]['set'], 'Reviewed set')
            self.assertEqual(item['editorial']['legacy_record']['playback']['sets'], [])
            for action in ['acquisition', 'download', 'schedule', 'public', 'relay']:
                self.assertFalse(review[action])

    def test_unreviewed_metadata_and_differing_observations_remain_separate(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp), approved=False, different=True)
            plan = prepare(*args)
            self.assertEqual(plan['private_playback_recordings'], 0)
            self.assertEqual(len(plan['sources']), 2)
            self.assertFalse(plan['snapshot_mappings'][0]['retained_file_matches_database'])
            self.assertFalse((args[3] / 'review-recording.json').exists())
            self.assertFalse((args[3] / 'audio-recording.json').exists())

    def test_exact_public_approval_and_relay_are_retained(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            data = json.loads(args[1].read_text())
            row = data['recordings'][0]
            row['review']['kind'] = 'audience'
            data['public_approvals'] = [{'recording_id': row['id'], 'approved': True,
                'source_hash': row['source_hash'], 'media_sha256': row['media_sha256'],
                'policy_basis': 'Reviewed noncommercial pilot', 'reviewer': 'Owner'}]
            args[1].write_text(json.dumps(data))
            receipt = json.loads(args[2].read_text())
            receipt['counts'] = {key: len(value) for key, value in data.items()}
            args[2].write_text(json.dumps(receipt))
            plan = prepare(*args, retain_public=True)
            review = json.loads((args[3] / 'review-recording.json').read_text())['review']
            self.assertEqual(plan['public_playback_recordings'], 1)
            for action in ['public', 'playback', 'relay', 'schedule']:
                self.assertTrue(review[action])
            self.assertFalse(review['download'])
            self.assertFalse(review['acquisition'])
            retained = json.loads((args[3] / 'retained-export.json').read_text())
            self.assertEqual(retained['tables'], data)

    def test_stale_public_approval_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            data = json.loads(args[1].read_text())
            row = data['recordings'][0]
            row['review']['kind'] = 'audience'
            data['public_approvals'] = [{'recording_id': row['id'], 'approved': True,
                'source_hash': row['source_hash'], 'media_sha256': '0' * 64,
                'policy_basis': 'Reviewed pilot', 'reviewer': 'Owner'}]
            args[1].write_text(json.dumps(data))
            receipt = json.loads(args[2].read_text())
            receipt['counts'] = {key: len(value) for key, value in data.items()}
            args[2].write_text(json.dumps(receipt))
            with self.assertRaisesRegex(ValueError, 'public approval does not match'):
                prepare(*args, retain_public=True)
            self.assertFalse(args[3].exists())

    def test_corrupt_original_fails_before_allocating_output(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.fixture(Path(temp))
            (args[0] / 'data/originals/song.mp3').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'backup file differs'):
                prepare(*args)
            self.assertFalse(args[3].exists())


if __name__ == '__main__':
    unittest.main()
