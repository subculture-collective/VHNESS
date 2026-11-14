import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import propose_release_upgrades
from propose_release_upgrades import propose_all
from upgrade_consumer import select_consumer

REGISTRY = json.loads((Path(__file__).resolve().parents[1] / 'docs/reference/consumer-registry.json').read_text())
CONSUMERS = REGISTRY['consumers']


def lane(consumer):
    return 'api' if consumer.get('mode') == 'api' else 'viewer' if consumer.get('mode') == 'viewer' else 'combined'


class RegistryTests(unittest.TestCase):
    def test_each_repository_has_at_most_one_entry_per_lane(self):
        keys = [(c['repository'], lane(c)) for c in CONSUMERS]
        self.assertEqual(len(keys), len(set(keys)))

    def test_every_viewer_consumer_also_receives_core_upgrades(self):
        api = {c['repository']: c for c in CONSUMERS if lane(c) == 'api'}
        for viewer in (c for c in CONSUMERS if lane(c) == 'viewer'):
            core = api.get(viewer['repository'])
            self.assertIsNotNone(core, viewer['repository'] + ' has no core lane')
            # The core lane writes the pins the viewer lane reads as its retained core.
            self.assertEqual((core['release_file'], core['engine_file']), (viewer['core_file'], viewer['core_engine_file']))
            self.assertEqual(core['adoption'], 'core')
            self.assertTrue(core['checks'])
            self.assertFalse(core.get('package_file') or core.get('lock_file'))
            for release in ({'artifact_kind': 'core'}, {'compatible_core_receipt': 'core-release-0.4.0.json'}):
                select_consumer(REGISTRY, viewer['repository'], release)

    def test_chicago_receives_core_upgrades(self):
        chicago = select_consumer(REGISTRY, 'subculture-collective/chicago-labor-archive', {'artifact_kind': 'core'})
        self.assertEqual(chicago['release_file'], 'deploy/core-release.json')
        self.assertEqual(chicago['engine_file'], 'deploy/archive-core.env')

    def test_archived_template_is_not_registered(self):
        self.assertNotIn('subculture-collective/video-archive-template', {c['repository'] for c in CONSUMERS})

    def test_lineage_uses_its_github_name(self):
        repositories = {c['repository'] for c in CONSUMERS}
        self.assertIn('subculture-collective/lineage', repositories)
        self.assertNotIn('PatrickFanella/arkhiv-live-music', repositories)
        for consumer in CONSUMERS:
            self.assertNotIn('github_repository', consumer, 'register ' + consumer['repository'] + ' under its GitHub name')


class ProposeAllTests(unittest.TestCase):
    def test_archived_consumer_is_skipped_before_clone_and_others_proceed(self):
        registry = {'consumers': [
            {'repository': 'org/archived', 'mode': 'api', 'adoption': 'core'},
            {'repository': 'org/live', 'mode': 'api', 'adoption': 'core'},
            {'repository': 'org/viewer', 'mode': 'viewer', 'adoption': 'package'},
        ]}
        requested = []

        def request(path, method='GET', payload=None):
            requested.append(path)
            return {'archived': path == '/repos/org/archived'}

        with tempfile.TemporaryDirectory() as work, \
                mock.patch.object(propose_release_upgrades.subprocess, 'run') as run, \
                mock.patch.object(propose_release_upgrades, 'proposal', return_value=None) as proposal, \
                mock.patch.dict('os.environ', {}, clear=False) as environment:
            environment.pop('ARCHIVE_UPGRADE_DATABASE_CONTAINER', None)
            failed = propose_all(registry, {'version': '0.4.10'}, 'core', None, Path(work), request=request)
        self.assertEqual(failed, [])
        self.assertEqual(requested, ['/repos/org/archived', '/repos/org/live'])
        cloned = [call.args[0][-2] for call in run.call_args_list]
        self.assertEqual(cloned, ['https://github.com/org/live.git'])
        self.assertEqual([call.args[1]['repository'] for call in proposal.call_args_list], ['org/live'])

    def test_metadata_failure_counts_against_that_consumer_only(self):
        registry = {'consumers': [
            {'repository': 'org/broken', 'mode': 'api', 'adoption': 'core'},
            {'repository': 'org/live', 'mode': 'api', 'adoption': 'core'},
        ]}

        def request(path, method='GET', payload=None):
            if path == '/repos/org/broken':
                raise ConnectionError('unreachable')
            return {'archived': False}

        with tempfile.TemporaryDirectory() as work, \
                mock.patch.object(propose_release_upgrades.subprocess, 'run'), \
                mock.patch.object(propose_release_upgrades, 'proposal', return_value='https://example.invalid/pull/1') as proposal, \
                mock.patch.dict('os.environ', {}, clear=False) as environment:
            environment.pop('ARCHIVE_UPGRADE_DATABASE_CONTAINER', None)
            failed = propose_all(registry, {'version': '0.4.10'}, 'core', None, Path(work), request=request)
        self.assertEqual(failed, ['org/broken'])
        self.assertEqual(proposal.call_count, 1)


if __name__ == '__main__':
    unittest.main()
