import base64
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from download_release_package import REGISTRY
from propose_consumer_upgrade import proposal
from upgrade_consumer import prepare, select_consumer


class ViewerUpgradeTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.artifact = self.root / 'viewer.tgz'
        self.artifact.write_bytes(b'qualified-viewer-artifact')
        self.core = json.loads((Path(__file__).resolve().parents[1] / 'docs/releases/core-release-0.3.0.json').read_text())
        self.current = {'package': '@subcult/archive-viewer', 'version': '0.4.1',
                        'source_revision': 'a' * 40, 'package_file': 'subcult-archive-viewer-0.4.1.tgz',
                        'package_sha256': hashlib.sha256(self.artifact.read_bytes()).hexdigest(),
                        'license': 'GPL-3.0-only', 'compatible_core_receipt': 'core-release-0.3.0.json',
                        'registry': REGISTRY}
        self.release = {**self.current, 'version': '0.4.2', 'package_file': 'subcult-archive-viewer-0.4.2.tgz', 'source_revision': 'b' * 40}
        self.consumer = {'repository': 'subculture-collective/example', 'mode': 'viewer',
                         'adoption': 'package', 'rollout': 'reviewed-pr', 'media': ['print'],
                         'modules': {'catalog': '1', 'print': '1'}, 'release_file': 'viewer.json',
                         'package_file': 'package.json', 'lock_file': 'pnpm-lock.yaml',
                         'core_file': 'core.json', 'core_engine_file': 'core.env', 'checks': [['consumer-check']]}
        for name, value in [('viewer.json', self.current), ('core.json', self.core),
                            ('package.json', {'dependencies': {self.current['package']: '0.4.1'}})]:
            (self.root / name).write_text(json.dumps(value))
        (self.root / 'core.env').write_text('ARCHIVE_ENGINE_IMAGE=' + self.core['engine_digest'] + '\n')
        (self.root / 'native-backend.env').write_text('NATIVE_IMAGE=owned-native-backend\n')
        (self.root / 'pnpm-lock.yaml').write_text('old lock\n')
        (self.root / 'brand.html').write_text('<h1>Owned frontend</h1>')
        self.git('init', '-q')
        self.commit()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, text=True).strip()

    def commit(self):
        self.git('add', '.')
        self.git('-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture')

    def bytes(self):
        return {p.name: p.read_bytes() for p in self.root.iterdir() if p.is_file()}

    def test_sdk_lane_and_upgrade_preserve_core_native_backend_and_brand(self):
        registry = {'consumers': [self.consumer, {'repository': self.consumer['repository'], 'mode': 'api'}]}
        self.assertIs(select_consumer(registry, self.consumer['repository'], self.release), self.consumer)
        before = self.bytes()
        def run(command, **kwargs):
            if command[0] == 'pnpm':
                (self.root / 'pnpm-lock.yaml').write_text('new lock\n')
        self.assertTrue(prepare(self.root, self.consumer, self.release, self.artifact, run))
        changed = {name for name, content in before.items() if (self.root / name).read_bytes() != content}
        self.assertEqual(changed, {'viewer.json', 'package.json', 'pnpm-lock.yaml'})
        self.commit()
        self.assertFalse(prepare(self.root, self.consumer, self.release, self.artifact, mock.Mock(side_effect=AssertionError)))

    def test_incompatible_receipts_and_bad_paths_fail_before_mutation(self):
        before = self.bytes()
        for key, value in [('compatible_core_receipt', 'core-release-0.2.0.json'), ('registry', 'https://elsewhere.invalid/'),
                           ('version', '1.0.0'), ('source_revision', 'short'), ('package_sha256', 'a' * 64),
                           ('license', 'other'), ('version', '0.4.1')]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                prepare(self.root, self.consumer, {**self.release, key: value}, self.artifact)
        for key, value in [('core_file', '../core.json'), ('core_file', 'viewer.json'), ('engine_file', 'core.env')]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                prepare(self.root, {**self.consumer, key: value}, self.release, self.artifact)
        self.assertEqual(self.bytes(), before)

    def test_core_api_modules_and_digest_are_required(self):
        for key, value in [('api_versions', ['2']), ('modules', {'catalog': '1', 'print': '2'}),
                           ('media', ['audio']), ('engine_digest', 'mutable:latest')]:
            (self.root / 'core.json').write_text(json.dumps({**self.core, key: value}))
            self.commit()
            with self.subTest(key=key), self.assertRaises(ValueError):
                prepare(self.root, self.consumer, self.release, self.artifact)

    def test_additive_core_upgrade_preserves_original_sdk_qualification(self):
        newer = {**self.core, 'version': '0.4.0', 'source_revision': 'c' * 40,
                 'engine_digest': self.core['engine_repository'] + '@sha256:' + 'd' * 64,
                 'migrations': {**self.core['migrations'], '0014_source_catalog.sql': 'e' * 64}}
        (self.root / 'core.json').write_text(json.dumps(newer))
        (self.root / 'core.env').write_text('ARCHIVE_ENGINE_IMAGE=' + newer['engine_digest'] + '\n')
        self.commit()
        self.assertTrue(prepare(self.root, self.consumer, self.release, self.artifact, mock.Mock()))
        self.assertEqual(json.loads((self.root / 'viewer.json').read_text())['compatible_core_receipt'],
                         'core-release-0.3.0.json')
        self.assertEqual(json.loads((self.root / 'core.json').read_text()), newer)

    def test_newer_core_rejects_changed_qualified_migration_and_major_version(self):
        for change in [{'version': '1.0.0'},
                       {'version': '0.4.0', 'migrations': {**self.core['migrations'], '0001_metadata.sql': 'f' * 64}}]:
            (self.root / 'core.json').write_text(json.dumps({**self.core, **change}))
            self.commit()
            before = self.bytes()
            with self.assertRaises(ValueError):
                prepare(self.root, self.consumer, self.release, self.artifact, mock.Mock())
            self.assertEqual(self.bytes(), before)

    def test_failed_or_pin_mutating_checks_restore_all_registered_files(self):
        before = self.bytes()
        for outcome in ['failure', 'changed-core']:
            def run(command, **kwargs):
                (self.root / 'pnpm-lock.yaml').write_text('partial')
                if command[0] == 'consumer-check':
                    (self.root / 'core.env').write_text('changed core')
                    if outcome == 'failure':
                        raise subprocess.CalledProcessError(1, command)
            with self.subTest(outcome=outcome), self.assertRaises((ValueError, subprocess.CalledProcessError)):
                prepare(self.root, self.consumer, self.release, self.artifact, run)
            self.assertEqual(self.bytes(), before)

    def test_sdk_proposals_stage_only_three_files_and_reconcile_repeated_delivery(self):
        posts = []
        branch = 'archive-viewer-upgrade/v0.4.2-' + 'b' * 40
        def request(path, method='GET', payload=None):
            if path == '/user':
                return {'login': 'subcult-agent'}
            if method == 'POST':
                posts.append(payload)
                return {'html_url': 'https://git.subcult.tv/example/pulls/1'}
            if '/pulls?' in path:
                return [] if not posts else [{'head': {'ref': branch}, 'base': {'ref': 'main'}, 'state': 'open', 'html_url': 'https://git.subcult.tv/example/pulls/1'}]
            if '/contents/' in path:
                return {'content': base64.b64encode(json.dumps(self.release).encode()).decode()}
            return {'default_branch': 'main', 'permissions': {'push': True}}
        with mock.patch('propose_consumer_upgrade.prepare'), mock.patch('propose_consumer_upgrade.subprocess.run') as run:
            for _ in range(2):
                self.assertEqual(proposal(self.release, self.consumer, self.root, self.artifact, request), 'https://git.subcult.tv/example/pulls/1')
        self.assertEqual(len(posts), 1)
        self.assertIn('independent viewer', posts[0]['body'])
        self.assertEqual([call.args[0] for call in run.call_args_list if call.args[0][:2] == ['git', 'add']],
                         [['git', 'add', '--', 'package.json', 'pnpm-lock.yaml', 'viewer.json']])
