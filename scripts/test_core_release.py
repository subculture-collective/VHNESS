import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from core_release import manifest, validate_core
from upgrade_consumer import prepare
from propose_consumer_upgrade import proposal


class CoreReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.consumer = {'repository': 'subculture-collective/http-client',
                         'mode': 'api', 'adoption': 'core', 'rollout': 'reviewed-pr',
                         'media': ['audio', 'print'], 'modules': {'catalog': '1', 'audio': '1', 'print': '1'},
                         'release_file': 'core.json', 'engine_file': 'core.env',
                         'checks': [['python3', 'check.py']]}
        self.current = {'contract_version': 1, 'artifact_kind': 'core',
                        'configuration_version': 1, 'api_versions': ['1'],
                        'media': ['print', 'audio', 'video'],
                        'modules': {'catalog': '1', 'print': '1', 'audio': '1', 'video': '1'},
                        'engine_repository': 'git.subcult.tv/subculture-collective/archive-core',
                        'schema_strategy': 'checksum-locked-additive-migrations',
                        'version': '0.1.0', 'source_revision': 'a' * 40,
                        'engine_digest': 'git.subcult.tv/subculture-collective/archive-core@sha256:' + 'a' * 64,
                        'migrations': {'0001.sql': 'a' * 64}}
        self.release = {**self.current, 'version': '0.1.1', 'source_revision': 'b' * 40,
                        'engine_digest': self.current['engine_repository'] + '@sha256:' + 'b' * 64}
        (self.root / 'core.json').write_text(json.dumps(self.current))
        (self.root / 'core.env').write_text('ARCHIVE_ENGINE_IMAGE=' + self.current['engine_digest'] + '\n')
        (self.root / 'custom.html').write_text('<h1>My own frontend</h1>')
        self.git('init', '-q')
        self.commit()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, text=True).strip()

    def commit(self):
        self.git('add', '.')
        self.git('-c', 'user.name=Test', '-c', 'user.email=test@example.invalid',
                 '-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture')

    def test_no_package_required_checks_run_and_repeat_is_noop(self):
        calls = []
        self.assertTrue(prepare(self.root, self.consumer, self.release, None,
                                lambda cmd, **kwargs: calls.append(cmd)))
        self.assertEqual(calls, [['python3', 'check.py']])
        self.assertEqual((self.root / 'custom.html').read_text(), '<h1>My own frontend</h1>')
        self.assertEqual((self.root / 'core.env').read_text(), 'ARCHIVE_ENGINE_IMAGE=' + self.release['engine_digest'] + '\n')
        self.commit()
        self.assertFalse(prepare(self.root, self.consumer, self.release, None))

    def test_failed_consumer_check_restores_both_core_files(self):
        before = {p.name: p.read_bytes() for p in self.root.iterdir() if p.is_file()}
        with self.assertRaises(subprocess.CalledProcessError):
            prepare(self.root, self.consumer, self.release, None,
                    mock.Mock(side_effect=subprocess.CalledProcessError(1, 'check')))
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.root.iterdir() if p.is_file()})

    def test_incompatible_or_reused_artifact_fails_before_mutation(self):
        for key, value in [('api_versions', ['2']), ('modules', {'catalog': '2'}),
                           ('engine_digest', self.current['engine_repository'] + ':latest'),
                           ('artifact_kind', 'reference'), ('version', '1.0.0'),
                           ('version', '0.1.0'), ('migrations', {'0001.sql': 'b' * 64}),
                           ('engine_repository', 'other.example/core'), ('media', ['video'])]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                prepare(self.root, self.consumer, {**self.release, key: value}, None)
        self.assertEqual(self.git('status', '--porcelain'), '')

    def test_api_mode_refuses_package_files_and_wrong_pin(self):
        with self.assertRaisesRegex(ValueError, 'frontend package'):
            validate_core(self.release, {**self.consumer, 'package_file': 'package.json'}, self.current)
        (self.root / 'core.env').write_text('ARCHIVE_ENGINE_IMAGE=unreviewed\n')
        self.commit()
        with self.assertRaisesRegex(ValueError, 'pin differs'):
            prepare(self.root, self.consumer, self.release, None)

    def test_proposal_contains_only_core_files_and_provenance(self):
        posts = []
        def request(path, method='GET', payload=None):
            if path == '/user':
                return {'login': 'subcult-agent'}
            if method == 'POST':
                posts.append(payload)
                return {'html_url': 'https://git.subcult.tv/http-client/pulls/1'}
            return [] if '/pulls?' in path else {'default_branch': 'main', 'permissions': {'push': True}}
        with mock.patch('propose_consumer_upgrade.prepare'), mock.patch('propose_consumer_upgrade.subprocess.run') as run:
            result = proposal(self.release, self.consumer, self.root, None, request)
        self.assertEqual(result, 'https://git.subcult.tv/http-client/pulls/1')
        self.assertIn('archive-core-upgrade/v0.1.1-', posts[0]['head'])
        self.assertIn(self.release['engine_digest'], posts[0]['body'])
        adds = [c.args[0] for c in run.call_args_list if c.args[0][:2] == ['git', 'add']]
        self.assertEqual(adds, [['git', 'add', '--', 'core.json', 'core.env']])

    def test_manifest_uses_clean_source_without_frontend_metadata(self):
        (self.root / 'docs/reference').mkdir(parents=True)
        contract = {k: v for k, v in self.current.items() if k not in ('version', 'source_revision', 'engine_digest', 'migrations')}
        (self.root / 'docs/reference/core-release-contract.json').write_text(json.dumps(contract))
        (self.root / 'docs/reference/core-version.json').write_text('{"version":"0.1.0"}')
        (self.root / 'migrations').mkdir()
        (self.root / 'migrations/0001.sql').write_text('SELECT 1;')
        self.commit()
        receipt = manifest(self.root, self.current['engine_digest'])
        self.assertEqual(receipt['source_revision'], self.git('rev-parse', 'HEAD'))
        self.assertEqual(len(receipt['migrations']['0001.sql']), 64)
        self.assertNotIn('package', receipt)
        (self.root / 'migrations/0001.sql').write_text('SELECT 2;')
        with self.assertRaisesRegex(ValueError, 'clean committed'):
            manifest(self.root, self.current['engine_digest'])


if __name__ == '__main__':
    unittest.main()
