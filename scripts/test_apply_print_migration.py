import copy
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.request

from apply_print_migration import ApplyError, PsqlState, apply, libpq_env, plan_steps
from prepare_print_migration import prepare

COLLECTION = 'fixture-zines'
DOCUMENT, UNPUBLISHED = 'd' * 64, 'a' * 64
READ, PRINT, DRAFT = 'e' * 64, 'f' * 64, 'b' * 64


def build_fixture(root):
    """Write a synthetic restored print pilot and prepare its inputs. Returns (inputs, archive)."""
    archive = root / 'archive'
    (archive / 'snapshots').mkdir(parents=True)
    files, assets = {}, []
    for name in ('read.pdf', 'print.pdf', 'draft.pdf'):
        data = ('%PDF synthetic ' + name).encode()
        key = hashlib.sha256(data).hexdigest()
        path = 'originals/' + key[:2] + '/' + key
        (archive / path).parent.mkdir(parents=True, exist_ok=True)
        (archive / path).write_bytes(data)
        files[path] = key
        assets.append({'sha256': key, 'size': len(data)})
    source = {'metadata': {'identifier': 'fixture-zine-source', 'title': 'Synthetic zines'},
              'files': [{'name': n, 'size': str(a['size'])} for n, a in zip(('read.pdf', 'print.pdf', 'draft.pdf'), assets)]}
    legacy_id = hashlib.sha256(json.dumps(source).encode()).hexdigest()
    raw = json.dumps(source, indent=2).encode()
    (archive / 'snapshots' / (legacy_id + '.json')).write_bytes(raw)
    files['snapshots/' + legacy_id + '.json'] = hashlib.sha256(raw).hexdigest()
    cover = 'derivatives/' + assets[0]['sha256'] + '/cover.png'
    (archive / cover).parent.mkdir(parents=True)
    (archive / cover).write_bytes(b'synthetic cover')
    files[cover] = hashlib.sha256(b'synthetic cover').hexdigest()
    dump = b'synthetic legacy dump'
    (root / 'catalog.dump').write_bytes(dump)
    document = {'collection_id': COLLECTION, 'source_item': 'fixture-zine-source', 'overrides': {},
                'rights': 'Synthetic private fixture', 'review_note': 'Synthetic reading decision'}
    catalog = {
        'assets': assets,
        'collections': [{'id': COLLECTION, 'config': {'name': 'Fixture Zines', 'accent': '#b3261e',
                                                       'introduction': 'Synthetic retained print collection',
                                                       'limits': {'total_bytes': 157286400}}}],
        'documents': [{**document, 'id': DOCUMENT, 'published': True, 'metadata': {'title': 'Fixture zine', 'date': '1994-05-01'}},
                      {**document, 'id': UNPUBLISHED, 'published': False, 'metadata': {'title': 'Unpublished draft'}}],
        'variants': [{'id': READ, 'document_id': DOCUMENT, 'filename': 'read.pdf', 'asset_sha': assets[0]['sha256'], 'source_snapshot': legacy_id, 'kind': 'read'},
                     {'id': PRINT, 'document_id': DOCUMENT, 'filename': 'print.pdf', 'asset_sha': assets[1]['sha256'], 'source_snapshot': legacy_id, 'kind': 'print'},
                     {'id': DRAFT, 'document_id': UNPUBLISHED, 'filename': 'draft.pdf', 'asset_sha': assets[2]['sha256'], 'source_snapshot': legacy_id, 'kind': 'draft'}],
        'source_snapshots': [{'id': legacy_id, 'item_id': 'fixture-zine-source', 'raw': source}],
        'asset_companions': [],
        'derivatives': [{'asset_sha': assets[0]['sha256'], 'status': 'page-text', 'pages': 2, 'tool': 'synthetic tool'}],
        'page_text': [{'asset_sha': assets[0]['sha256'], 'page': 1, 'text': 'Solidarity on page one'},
                      {'asset_sha': assets[0]['sha256'], 'page': 2, 'text': 'Mutual aid on page two'}],
    }
    (root / 'export.json').write_text(json.dumps(catalog))
    dump_hash = hashlib.sha256(dump).hexdigest()
    (root / 'complete.json').write_text(json.dumps({'complete': True, 'source': 'backup', 'catalog_sha256': dump_hash, 'catalog_size': len(dump), 'files': files}))
    (root / 'restore.json').write_text(json.dumps({'restore_exit_code': 0, 'dump_sha256': dump_hash,
                                                   'table_counts': {t: len(rows) for t, rows in catalog.items()}}))
    prepare(root, root / 'export.json', root / 'restore.json', root / 'inputs')
    return root / 'inputs', archive


class FakeDatabase:
    """Applies operator commands to in-memory state and answers the same checks as PsqlState."""

    def __init__(self):
        self.calls, self.fail, self.tables = [], None, {
            'collections': {}, 'retained': set(), 'snapshots': {}, 'items': {}, 'reviews': {}, 'indexes': {}}

    def run(self, argv):
        self.calls.append(argv)
        command, flags = argv[0], {}
        rest = argv[1:]
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
            t['retained'].add((flags['collection'], hashlib.sha256(data).hexdigest()))
        elif command == 'import-source':
            t['snapshots'][hashlib.sha256(data).hexdigest()] = flags['id']
        elif command == 'import-catalog':
            item = json.loads(data)
            if item['id'] in t['items']:
                return 1  # duplicate primary key
            reps = []
            for r in sorted(item['representations'], key=lambda r: r['id']):
                extension = copy.deepcopy(r['extension'])
                if not extension['print'].get('companions'):
                    extension['print'].pop('companions', None)  # omitempty, as the Go encoder stores it
                reps.append({'id': r['id'], 'media': r['media'], 'source_item_id': r['source_item_id'],
                             'snapshot_sha256': r['snapshot_sha256'], 'file': r['file'],
                             'collections': sorted(r['collections']), 'extension': extension})
            t['items'][item['id']] = {'title': item['title'], 'description': item['description'], 'date': item['date'],
                                      'creator': item['creator'], 'editorial': item['editorial'],
                                      'collections': sorted(item['collections']), 'representations': reps}
        elif command == 'review-transport':
            review = json.loads(data)
            if review['review']['public'] != flags.get('public', False):
                return 1
            t['reviews'][(flags['id'], flags['representation'], flags['collection'])] = review
        elif command == 'restore-print-index':
            index = json.loads(data)
            stored = {k: v for k, v in index.items() if k not in ('sections', 'independent_recovery_sha256')}
            stored['sections'] = {k: v for k, v in index['sections'].items() if v.strip()}
            t['indexes'][(flags['id'], flags['representation'])] = stored
        return 0

    def collection(self, collection):
        return self.tables['collections'].get(collection)

    def retained_export(self, collection, sha256):
        return (collection, sha256) in self.tables['retained']

    def snapshot(self, sha256):
        return self.tables['snapshots'].get(sha256)

    def catalog_item(self, item):
        return self.tables['items'].get(item)

    def transport_review(self, item, representation, collection):
        return self.tables['reviews'].get((item, representation, collection))

    def print_index(self, item, representation):
        return self.tables['indexes'].get((item, representation))


class ApplyPrintMigrationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.inputs, archive = build_fixture(self.root)
        self.env = {'VHNESS_MEDIA': str(archive), 'VHNESS_DERIVATIVES': str(archive)}
        self.db = FakeDatabase()
        self.receipts = 0

    def apply(self, **options):
        self.receipts += 1
        options.setdefault('budget', 1 << 20)
        options.setdefault('reserve', 1)
        receipt = self.root / ('receipt-%d.json' % self.receipts)
        result = apply(self.inputs, self.db.run, self.db, receipt, environ=self.env, log=lambda *_: None, **options)
        self.assertEqual(json.loads(receipt.read_text()), result)
        return result

    def commands(self):
        return [call[0] for call in self.db.calls]

    def test_applies_every_input_in_order_with_legacy_identities(self):
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'complete')
        self.assertEqual(self.commands(), ['migrate', 'collection', 'retain-legacy', 'import-source',
                                           'import-catalog', 'import-catalog', 'review-transport',
                                           'review-transport', 'restore-print-index'])
        self.assertEqual({k: [r['id'] for r in v['representations']] for k, v in self.db.tables['items'].items()},
                         {DOCUMENT: sorted([READ, PRINT]), UNPUBLISHED: [DRAFT]})
        self.assertEqual(sorted(k[1] for k in self.db.tables['reviews']), sorted([READ, PRINT]))
        self.assertIn((DOCUMENT, READ), self.db.tables['indexes'])
        self.assertEqual(receipt['counts'], {'applied': 8, 'unchanged': 0, 'ran': 1, 'failed': 0})
        retain = next(c for c in self.db.calls if c[0] == 'retain-legacy')
        self.assertIn('--notes', retain)

    def test_second_run_changes_nothing(self):
        self.apply()
        before = copy.deepcopy(self.db.tables)
        self.db.calls.clear()
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'complete')
        self.assertEqual(self.commands(), ['migrate'])
        self.assertEqual(self.db.tables, before)
        self.assertEqual(receipt['counts'], {'applied': 0, 'unchanged': 8, 'ran': 1, 'failed': 0})

    def test_stops_at_first_failure_and_resumes(self):
        self.db.fail = 'import-catalog'
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('import-catalog', receipt['failure'])
        self.assertIn('exit code 1', receipt['failure'])
        self.assertEqual(receipt['steps'][-1]['outcome'], 'failed')
        self.assertNotIn('review-transport', self.commands())
        self.db.fail, self.db.calls = None, []
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'complete')
        self.assertEqual(self.commands().count('import-source'), 0)
        self.assertEqual(self.commands().count('import-catalog'), 2)

    def test_differing_current_review_is_not_overwritten(self):
        self.apply()
        key = next(iter(self.db.tables['reviews']))
        self.db.tables['reviews'][key]['review']['read'] = False
        self.db.calls.clear()
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('review it explicitly', receipt['failure'])
        self.assertNotIn('review-transport', self.commands())
        self.assertFalse(self.db.tables['reviews'][key]['review']['read'])

    def test_changed_catalog_item_is_not_reported_unchanged(self):
        self.apply()
        path = self.inputs / ('item-' + UNPUBLISHED + '.json')
        item = json.loads(path.read_text())
        item['title'] = 'Changed title'
        item['editorial']['legacy_document']['published'] = True
        path.write_text(json.dumps(item))
        self.db.calls.clear()
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('catalog item differs', receipt['failure'])
        self.assertNotIn('import-catalog', self.commands())

    def test_plan_without_limits_needs_explicit_limits(self):
        plan_file = self.inputs / 'migration-plan.json'
        plan = json.loads(plan_file.read_text())
        del plan['collection_limits']  # prepared before plans recorded legacy limits
        plan_file.write_text(json.dumps(plan))
        receipt = self.apply(budget=None, reserve=None)
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('--budget and --reserve', receipt['failure'])
        self.assertEqual(self.commands(), ['migrate'])

    def test_existing_collection_with_other_limits_stops(self):
        self.db.tables['collections'][COLLECTION] = {'policy': 'local_preserve', 'budget': 5, 'reserve': 1}
        receipt = self.apply()
        self.assertIn('review-budget', receipt['failure'])
        self.assertEqual(self.commands(), ['migrate'])

    def test_plan_limits_create_the_collection(self):
        receipt = self.apply(budget=None, reserve=None)
        self.assertEqual(receipt['status'], 'complete', receipt.get('failure'))
        self.assertEqual(self.db.tables['collections'][COLLECTION],
                         {'policy': 'local_preserve', 'budget': 157286400, 'reserve': 2 << 30})
        step = next(s for s in receipt['steps'] if s['command'] == 'collection')
        self.assertEqual((step['budget'], step['reserve'], step['limits_from']), (157286400, 2 << 30, 'plan'))
        rerun = self.apply(budget=None, reserve=None)
        self.assertEqual(rerun['counts']['applied'], 0)

    def test_budget_and_reserve_override_plan_limits(self):
        receipt = self.apply(budget=8 << 30, reserve=1 << 30)
        self.assertEqual(self.db.tables['collections'][COLLECTION]['budget'], 8 << 30)
        step = next(s for s in receipt['steps'] if s['command'] == 'collection')
        self.assertEqual((step['reserve'], step['limits_from']), (1 << 30, 'override'))
        # A later run on the plan's own limits stops instead of changing the collection.
        receipt = self.apply(budget=None, reserve=None)
        self.assertIn('review-budget', receipt['failure'])

    def test_each_collection_gets_its_own_plan_limits(self):
        plan_file = self.inputs / 'migration-plan.json'
        plan = json.loads(plan_file.read_text())
        plan['collections']['a-info-zines'] = {}
        plan['collection_limits']['a-info-zines'] = {'budget': 5, 'reserve': 3}
        plan_file.write_text(json.dumps(plan))
        retained = self.inputs / ('retained-export-' + COLLECTION + '.json')
        (self.inputs / 'retained-export-a-info-zines.json').write_bytes(retained.read_bytes())
        _, steps = plan_steps(self.inputs, 'notes')
        self.assertEqual({s['collection']: (s['budget'], s['reserve']) for s in steps if s['command'] == 'collection'},
                         {'a-info-zines': (5, 3), COLLECTION: (157286400, 2 << 30)})
        _, steps = plan_steps(self.inputs, 'notes', budget=7, reserve=1)
        self.assertEqual({(s['budget'], s['reserve']) for s in steps if s['command'] == 'collection'}, {(7, 1)})

    def test_index_restore_requires_recovered_roots(self):
        self.env = {}
        receipt = self.apply()
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('VHNESS_MEDIA and VHNESS_DERIVATIVES', receipt['failure'])
        self.assertEqual(self.db.calls, [])

    def test_public_review_uses_explicit_public_path(self):
        path = self.inputs / ('review-' + READ + '.json')
        review = json.loads(path.read_text())
        review['review']['public'] = True
        path.write_text(json.dumps(review))
        self.assertEqual(self.apply()['status'], 'complete')
        public = [c for c in self.db.calls if '--approve-public' in c]
        self.assertEqual([c[c.index('--representation') + 1] for c in public], [READ])

    def test_changed_source_input_is_rejected_before_any_command(self):
        next(self.inputs.glob('source-*.json')).write_text('{}')
        receipt = self.apply()
        self.assertIn('Source input differs', receipt['failure'])
        self.assertEqual(self.db.calls, [])

    def test_receipt_is_never_overwritten(self):
        receipt = self.root / 'receipt.json'
        receipt.write_text('kept')
        with self.assertRaises(FileExistsError):
            apply(self.inputs, self.db.run, self.db, receipt, budget=1, reserve=1, environ=self.env, log=lambda *_: None)
        self.assertEqual(receipt.read_text(), 'kept')

    def test_database_url_becomes_libpq_variables(self):
        env = libpq_env({'DATABASE_URL': 'postgres://user:p%40ss@db.internal:5433/arkhiv?sslmode=disable'})
        self.assertEqual((env['PGHOST'], env['PGPORT'], env['PGUSER'], env['PGPASSWORD'], env['PGDATABASE'], env['PGSSLMODE']),
                         ('db.internal', '5433', 'user', 'p@ss', 'arkhiv', 'disable'))

    def test_keyword_connection_string_becomes_libpq_variables(self):
        env = libpq_env({'DATABASE_URL': "host=/run/postgresql dbname=restored user=pilot password='it\\'s secret' sslmode=disable",
                         'PGHOST': 'elsewhere', 'PGPORT': '6000'})
        self.assertEqual((env['PGHOST'], env['PGDATABASE'], env['PGUSER'], env['PGPASSWORD'], env['PGSSLMODE']),
                         ('/run/postgresql', 'restored', 'pilot', "it's secret", 'disable'))
        self.assertEqual(env['PGPORT'], '6000')  # omitted from the DSN, so the inherited value applies, as for vhness
        self.assertEqual(libpq_env({'DATABASE_URL': r'user=a password=secret\ word\\x'})['PGPASSWORD'], 'secret word\\x')

    def test_url_keeps_inherited_settings_it_omits(self):
        env = libpq_env({'DATABASE_URL': 'postgres:///archive', 'PGHOST': 'db.internal', 'PGPORT': '6543'})
        self.assertEqual((env['PGHOST'], env['PGPORT'], env['PGDATABASE']), ('db.internal', '6543', 'archive'))

    def test_unsupported_connection_settings_are_rejected(self):
        for url in ('postgres://a,b/db', 'host=db unknown_setting=1', 'not a dsn', 'postgres://db/x?pool_max_conns=4'):
            with self.assertRaises(ApplyError):
                libpq_env({'DATABASE_URL': url})


@unittest.skipUnless(os.environ.get('ARKHIV_TEST_DATABASE_URL') and os.environ.get('VHNESS_BIN'),
                     'set ARKHIV_TEST_DATABASE_URL (an empty disposable database) and VHNESS_BIN')
class FixtureDatabaseTests(unittest.TestCase):
    """Applies the synthetic fixture to a real disposable database through the real commands."""

    def test_fixture_migration(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        inputs, archive = build_fixture(root)
        env = dict(os.environ, DATABASE_URL=os.environ['ARKHIV_TEST_DATABASE_URL'], VHNESS_COLLECTION=COLLECTION,
                   VHNESS_MEDIA=str(archive), VHNESS_DERIVATIVES=str(archive),
                   VHNESS_MIGRATIONS=str(Path(__file__).resolve().parent.parent / 'migrations'))
        for inherited in ('ARKHIV_PUBLIC', 'DATABASE_URL_FILE'):  # the file would win over the disposable URL
            env.pop(inherited, None)
        vhness = os.environ['VHNESS_BIN']
        state = PsqlState(['psql'], libpq_env(env))

        def run(argv):
            return subprocess.run([vhness] + argv, env=env).returncode

        def fingerprint():
            tables = state.query("SELECT json_agg(tablename ORDER BY tablename) FROM pg_tables WHERE schemaname='public';")
            return {t: state.query('SELECT json_build_object(\'rows\',count(*),\'md5\',md5(COALESCE(string_agg(x::text,\',\' ORDER BY x::text),\'\'))) FROM "%s" x;' % t) for t in tables}

        first = apply(inputs, run, state, root / 'first.json', environ=env)  # the plan's legacy limits
        self.assertEqual(first['status'], 'complete', first.get('failure'))
        before = fingerprint()
        second = apply(inputs, run, state, root / 'second.json', environ=env)
        self.assertEqual(second['status'], 'complete', second.get('failure'))
        self.assertEqual(second['counts']['applied'], 0)
        self.assertEqual(fingerprint(), before)
        for item, variants in ((DOCUMENT, [READ, PRINT]), (UNPUBLISHED, [DRAFT])):
            self.assertEqual([r['id'] for r in state.catalog_item(item)['representations']], sorted(variants))

        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        server = subprocess.Popen([vhness, 'serve'], env=dict(env, ARKHIV_COMPATIBILITY='print', API_ADDR='127.0.0.1:%d' % port))
        self.addCleanup(server.wait)
        self.addCleanup(server.terminate)
        base = 'http://127.0.0.1:%d' % port

        def get(path):
            for _ in range(100):
                try:
                    with urllib.request.urlopen(base + path, timeout=2) as response:
                        return json.loads(response.read())
                except OSError:
                    time.sleep(0.1)
            raise AssertionError('API unavailable: ' + path)

        self.assertEqual(get('/api/collections'), [{'id': COLLECTION, 'name': 'Fixture Zines', 'accent': '#b3261e',
                                                    'introduction': 'Synthetic retained print collection'}])
        documents = get('/api/documents')
        self.assertEqual([d['id'] for d in documents['documents']], [DOCUMENT])
        variants = {v['id']: v for v in documents['documents'][0]['variants']}
        self.assertEqual(sorted(variants), sorted([READ, PRINT]))
        self.assertEqual((variants[READ]['pages'], variants[READ]['text_status']), (2, 'page-text'))


if __name__ == '__main__':
    unittest.main()
