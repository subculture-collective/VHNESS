import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest

from apply_print_migration import PsqlState, apply, libpq_env
from export_print_legacy import TABLES, export, export_sql
from prepare_print_migration import prepare
from test_apply_print_migration import FakeDatabase

COLLECTION = 'roundtrip-zines'
PUBLISHED, DRAFT_DOCUMENT = '1' * 64, '2' * 64
READ, DRAFT = '3' * 64, '4' * 64

# The legacy print schema (migrations 0001-0006 of the retained pilot and its
# schema_versions table), for the disposable database round trip.
LEGACY_SCHEMA = """
CREATE TABLE schema_versions(name text PRIMARY KEY, sha256 text NOT NULL);
CREATE TABLE app_metadata (key text PRIMARY KEY, value text NOT NULL, updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE collections (id text PRIMARY KEY, config jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE source_snapshots (id text PRIMARY KEY, item_id text NOT NULL, raw jsonb NOT NULL, captured_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE assets (sha256 text PRIMARY KEY CHECK (sha256 ~ '^[a-f0-9]{64}$'), size bigint NOT NULL CHECK (size > 0), verified_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE documents (
 id text PRIMARY KEY, collection_id text NOT NULL REFERENCES collections(id), source_item text NOT NULL,
 source_key text NOT NULL, metadata jsonb NOT NULL, overrides jsonb NOT NULL DEFAULT '{}',
 published boolean NOT NULL DEFAULT false, review_note text NOT NULL DEFAULT '', rights text NOT NULL DEFAULT 'unknown',
 updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(collection_id, source_item, source_key));
CREATE TABLE variants (
 id text PRIMARY KEY, document_id text NOT NULL REFERENCES documents(id), filename text NOT NULL,
 asset_sha text NOT NULL REFERENCES assets(sha256), source_snapshot text NOT NULL REFERENCES source_snapshots(id),
 kind text NOT NULL DEFAULT 'original', public_approved boolean NOT NULL DEFAULT false, UNIQUE(document_id, filename, asset_sha));
CREATE TABLE acquisition_attempts (
 id bigserial PRIMARY KEY, collection_id text NOT NULL REFERENCES collections(id), source_item text NOT NULL,
 filename text NOT NULL, status text NOT NULL, detail text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE derivatives (
 asset_sha text PRIMARY KEY REFERENCES assets(sha256), status text NOT NULL, pages integer NOT NULL DEFAULT 0,
 tool text NOT NULL, error text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE page_text (
 asset_sha text NOT NULL REFERENCES assets(sha256), page integer NOT NULL CHECK(page >= 0), text text NOT NULL,
 search tsvector GENERATED ALWAYS AS (to_tsvector('simple',text)) STORED, PRIMARY KEY(asset_sha,page));
CREATE TABLE review_events (
 id bigserial PRIMARY KEY, document_id text NOT NULL REFERENCES documents(id), action text NOT NULL,
 detail jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE asset_companions (
 asset_sha text NOT NULL REFERENCES assets(sha256), companion_sha text NOT NULL REFERENCES assets(sha256),
 filename text NOT NULL, source_snapshot text NOT NULL REFERENCES source_snapshots(id), PRIMARY KEY(asset_sha,companion_sha));
CREATE TABLE source_inventories (
 collection_id text NOT NULL REFERENCES collections(id), source_item text NOT NULL,
 snapshot_id text NOT NULL REFERENCES source_snapshots(id), inventory jsonb NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT now(), scope text NOT NULL DEFAULT '', PRIMARY KEY(collection_id, source_item));
CREATE TABLE source_changes (
 id text PRIMARY KEY, collection_id text NOT NULL REFERENCES collections(id), source_item text NOT NULL,
 filename text NOT NULL, kind text NOT NULL, snapshot_id text NOT NULL REFERENCES source_snapshots(id),
 detail jsonb NOT NULL, decision text NOT NULL DEFAULT 'pending', review_note text NOT NULL DEFAULT '',
 document_id text REFERENCES documents(id), created_at timestamptz NOT NULL DEFAULT now(), reviewed_at timestamptz);
CREATE TABLE source_refresh_runs (
 id text PRIMARY KEY, collection_id text NOT NULL REFERENCES collections(id), started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz, status text NOT NULL, result jsonb NOT NULL DEFAULT '{}');
CREATE TABLE source_discoveries (
 collection_id text NOT NULL REFERENCES collections(id), scope text NOT NULL, raw jsonb NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(collection_id,scope));
CREATE TABLE public_access_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, variant_id text NOT NULL REFERENCES variants(id),
 asset_sha text NOT NULL REFERENCES assets(sha256), approved boolean NOT NULL,
 review text NOT NULL CHECK(length(trim(review)) > 0), manifest_sha256 text NOT NULL DEFAULT '',
 decided_at timestamptz NOT NULL DEFAULT now());
"""
# Fixed timestamps keep the export, and so the prepared inputs, identical on every run.
AT = '2026-10-03T00:00:00+00:00'
# Insert order that satisfies the foreign keys above.
INSERT_ORDER = ('collections', 'source_snapshots', 'assets', 'documents', 'variants', 'derivatives', 'page_text',
                'schema_versions', 'review_events', 'public_access_events')


def legacy_fixture(root):
    """Write a synthetic backup (catalog.dump and archive/) under root. Returns every legacy table's rows."""
    archive = root / 'archive'
    (archive / 'snapshots').mkdir(parents=True)
    assets = []
    for name in ('read.pdf', 'draft.pdf'):
        data = ('%PDF synthetic round trip ' + name).encode()
        key = hashlib.sha256(data).hexdigest()
        (archive / 'originals' / key[:2]).mkdir(parents=True, exist_ok=True)
        (archive / 'originals' / key[:2] / key).write_bytes(data)
        assets.append({'sha256': key, 'size': len(data), 'verified_at': AT})
    source = {'metadata': {'identifier': 'roundtrip-zine-source', 'title': 'Round trip zines'},
              'files': [{'name': n, 'size': str(a['size'])} for n, a in zip(('read.pdf', 'draft.pdf'), assets)]}
    legacy_id = hashlib.sha256(json.dumps(source).encode()).hexdigest()
    (archive / 'snapshots' / (legacy_id + '.json')).write_text(json.dumps(source, indent=2))
    cover = archive / 'derivatives' / assets[0]['sha256'] / 'cover.png'
    cover.parent.mkdir(parents=True)
    cover.write_bytes(b'synthetic round trip cover')
    (root / 'catalog.dump').write_bytes(b'synthetic legacy dump for the round trip')
    document = {'collection_id': COLLECTION, 'source_item': 'roundtrip-zine-source', 'overrides': {}, 'updated_at': AT,
                'rights': 'Synthetic private fixture', 'review_note': 'Synthetic reading decision'}
    tables = {table: [] for table in TABLES}
    tables.update({
        'assets': assets,
        'collections': [{'id': COLLECTION, 'config': {'name': 'Round Trip Zines', 'accent': '#245c42',
                                                       'introduction': 'Synthetic exported print collection'},
                         'updated_at': AT}],
        'documents': [{**document, 'id': PUBLISHED, 'source_key': 'read', 'published': True,
                       'metadata': {'title': 'Round trip zine', 'date': '1995-03-01'}},
                      {**document, 'id': DRAFT_DOCUMENT, 'source_key': 'draft', 'published': False,
                       'metadata': {'title': 'Unpublished round trip draft'}}],
        'variants': [{'id': READ, 'document_id': PUBLISHED, 'filename': 'read.pdf', 'asset_sha': assets[0]['sha256'],
                      'source_snapshot': legacy_id, 'kind': 'read', 'public_approved': False},
                     {'id': DRAFT, 'document_id': DRAFT_DOCUMENT, 'filename': 'draft.pdf', 'asset_sha': assets[1]['sha256'],
                      'source_snapshot': legacy_id, 'kind': 'draft', 'public_approved': False}],
        'source_snapshots': [{'id': legacy_id, 'item_id': 'roundtrip-zine-source', 'raw': source, 'captured_at': AT}],
        'derivatives': [{'asset_sha': assets[0]['sha256'], 'status': 'page-text', 'pages': 2, 'tool': 'synthetic tool',
                         'error': '', 'updated_at': AT}],
        'page_text': [{'asset_sha': assets[0]['sha256'], 'page': 1, 'text': 'Round trip page one'},
                      {'asset_sha': assets[0]['sha256'], 'page': 2, 'text': 'Round trip page two'}],
        'schema_versions': [{'name': '0001_init.sql', 'sha256': 'f' * 64}],
        'review_events': [],
    })
    return tables


class FakeLegacyDatabase:
    """Answers the export's two read-only statements from in-memory tables."""

    def __init__(self, tables):
        self.tables, self.page_text_columns, self.statements = tables, ['asset_sha', 'page', 'text'], []

    def query(self, sql):
        self.statements.append(sql)
        if 'information_schema' in sql:
            return {'tables': sorted(self.tables), 'page_text_columns': self.page_text_columns}
        return {'counts': {table: len(rows) for table, rows in self.tables.items()}, 'tables': self.tables}


class ExportPrintLegacyTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.tables = legacy_fixture(self.root)
        self.db = FakeLegacyDatabase(self.tables)
        self.output = self.root / 'export'

    def export(self, source='backup', code=0):
        return export(self.root, self.output, source, code, self.db.query, now=lambda: '2026-11-14T00:00:00+00:00')

    def prepare(self, **options):
        return prepare(self.root, self.output / 'restored-catalog.json', self.output / 'restore-receipt.json',
                       self.root / 'inputs', manifest=self.output / 'complete.json', **options)

    def test_round_trip_through_prepare_and_apply(self):
        result = self.export()
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode) & 0o077, 0)
        for name in ('complete.json', 'restored-catalog.json', 'restore-receipt.json'):
            path = self.output / name
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), result['outputs'][name])
        manifest = json.loads((self.output / 'complete.json').read_text())
        self.assertEqual(manifest['source'], 'backup')
        self.assertEqual(manifest['catalog_sha256'], hashlib.sha256((self.root / 'catalog.dump').read_bytes()).hexdigest())
        self.assertEqual(sorted(manifest['files']), sorted(str(p.relative_to(self.root / 'archive'))
                                                            for p in (self.root / 'archive').rglob('*') if p.is_file()))
        for name, value in manifest['files'].items():
            self.assertEqual(hashlib.sha256((self.root / 'archive' / name).read_bytes()).hexdigest(), value)
        receipt = json.loads((self.output / 'restore-receipt.json').read_text())
        self.assertEqual(receipt['table_counts'], {t: len(rows) for t, rows in self.tables.items()})
        self.assertEqual(json.loads((self.output / 'restored-catalog.json').read_text()), self.tables)
        self.assertTrue(all(sql.startswith('SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY;')
                            for sql in self.db.statements))

        plan = self.prepare()
        self.assertEqual((plan['documents'], plan['variants'], plan['cutover_evidence']), (2, 2, True))
        self.assertEqual(plan['legacy_export_sha256'], receipt['restored_catalog_sha256'])
        self.assertEqual([i['representation_id'] for i in plan['indexes']], [READ])

        core = FakeDatabase()
        archive = str(self.root / 'archive')
        applied = apply(self.root / 'inputs', core.run, core, self.root / 'receipt.json', budget=1 << 20, reserve=1,
                        environ={'VHNESS_MEDIA': archive, 'VHNESS_DERIVATIVES': archive}, log=lambda *_: None)
        self.assertEqual(applied['status'], 'complete', applied.get('failure'))
        self.assertEqual(sorted(core.tables['items']), [PUBLISHED, DRAFT_DOCUMENT])
        self.assertIn((PUBLISHED, READ), core.tables['indexes'])

    def test_live_source_is_recorded_and_refused_for_cutover(self):
        self.export(source='live')
        self.assertEqual(json.loads((self.output / 'complete.json').read_text())['source'], 'live')
        with self.assertRaisesRegex(ValueError, 'source is live, not an independent off-host backup'):
            self.prepare()
        plan = self.prepare(allow_live=True)
        self.assertEqual((plan['evidence_source'], plan['cutover_evidence']), ('live', False))

    def test_unknown_source_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'backup or live'):
            self.export(source='copy')
        self.assertFalse(self.output.exists())

    def test_failed_restore_is_not_exported(self):
        with self.assertRaisesRegex(ValueError, 'restore failed'):
            self.export(code=1)
        self.assertFalse(self.output.exists())

    def test_existing_output_is_never_reused(self):
        self.output.mkdir()
        (self.output / 'complete.json').write_text('kept')
        with self.assertRaisesRegex(ValueError, 'already exists'):
            self.export()
        self.assertEqual((self.output / 'complete.json').read_text(), 'kept')

    def test_other_schema_stops_before_output(self):
        self.tables['curation_drafts'] = []
        with self.assertRaisesRegex(ValueError, 'unknown: curation_drafts'):
            self.export()
        del self.tables['curation_drafts'], self.tables['page_text']
        with self.assertRaisesRegex(ValueError, 'missing: page_text'):
            self.export()
        self.assertFalse(self.output.exists())

    def test_extra_page_text_column_stops_before_output(self):
        self.db.page_text_columns = ['asset_sha', 'language', 'page', 'text']
        with self.assertRaisesRegex(ValueError, 'page_text columns'):
            self.export()
        self.assertFalse(self.output.exists())

    def test_count_mismatch_stops_before_output(self):
        query = self.db.query

        def short(sql):
            result = query(sql)
            if 'counts' in result:
                result = {'counts': dict(result['counts'], variants=3), 'tables': result['tables']}
            return result
        self.db.query = short
        with self.assertRaisesRegex(ValueError, 'table count: variants'):
            self.export()
        self.assertFalse(self.output.exists())

    def test_archive_link_stops_before_output(self):
        (self.root / 'archive' / 'linked.pdf').symlink_to(self.root / 'catalog.dump')
        with self.assertRaisesRegex(ValueError, 'link or special file: linked.pdf'):
            self.export()
        self.assertFalse(self.output.exists())

    def test_changed_table_export_is_rejected_by_prepare(self):
        self.export()
        path = self.output / 'restored-catalog.json'
        path.chmod(0o600)
        path.write_text(path.read_text().replace('Round trip zine', 'Edited zine'))
        with self.assertRaisesRegex(ValueError, 'differs from the one its restore receipt records'):
            self.prepare()

    def test_export_reads_every_table(self):
        sql = export_sql()
        for table in TABLES:
            self.assertIn("'%s',(SELECT count(*) FROM %s)" % (table, table), sql)
        self.assertNotIn('search', sql)


@unittest.skipUnless(os.environ.get('ARKHIV_TEST_LEGACY_DATABASE_URL') and os.environ.get('ARKHIV_TEST_DATABASE_URL')
                     and os.environ.get('VHNESS_BIN'),
                     'set ARKHIV_TEST_LEGACY_DATABASE_URL and ARKHIV_TEST_DATABASE_URL (disposable databases) and VHNESS_BIN')
class LegacyDatabaseRoundTripTests(unittest.TestCase):
    """Exports a synthetic legacy database, then prepares and applies it through the real commands."""

    def test_export_prepare_apply(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        tables = legacy_fixture(root)
        base = {k: v for k, v in os.environ.items() if k not in ('DATABASE_URL_FILE', 'ARKHIV_PUBLIC')}
        legacy = PsqlState(['psql'], libpq_env(dict(base, DATABASE_URL=os.environ['ARKHIV_TEST_LEGACY_DATABASE_URL'])))
        # The legacy database is disposable: rebuild the schema from nothing on every run.
        legacy.query(''.join('DROP TABLE IF EXISTS %s CASCADE;\n' % t for t in TABLES) + LEGACY_SCHEMA)
        for table in INSERT_ORDER:
            rows = tables[table]
            if rows:
                columns = ','.join(rows[0])
                legacy.query('INSERT INTO %s (%s) SELECT %s FROM json_populate_recordset(NULL::%s, :\'rows\');'
                             % (table, columns, columns, table), rows=json.dumps(rows))
        output = root / 'export'
        result = export(root, output, 'backup', 0, legacy.query)
        self.assertEqual(result['rows'], sum(len(rows) for rows in tables.values()))
        exported = json.loads((output / 'restored-catalog.json').read_text())
        self.assertEqual(exported['page_text'], tables['page_text'])
        self.assertEqual(exported['documents'][0], {**tables['documents'][0], 'updated_at': exported['documents'][0]['updated_at']})
        self.assertIs(exported['variants'][0]['public_approved'], False)
        prepare(root, output / 'restored-catalog.json', output / 'restore-receipt.json', root / 'inputs',
                manifest=output / 'complete.json')

        env = dict(base, DATABASE_URL=os.environ['ARKHIV_TEST_DATABASE_URL'], VHNESS_COLLECTION=COLLECTION,
                   VHNESS_MEDIA=str(root / 'archive'), VHNESS_DERIVATIVES=str(root / 'archive'),
                   VHNESS_MIGRATIONS=str(Path(__file__).resolve().parent.parent / 'migrations'))
        state = PsqlState(['psql'], libpq_env(env))

        def run(argv):
            return subprocess.run([os.environ['VHNESS_BIN']] + argv, env=env).returncode

        first = apply(root / 'inputs', run, state, root / 'first.json', budget=1 << 20, reserve=1, environ=env)
        self.assertEqual(first['status'], 'complete', first.get('failure'))
        second = apply(root / 'inputs', run, state, root / 'second.json', budget=1 << 20, reserve=1, environ=env)
        self.assertEqual(second['status'], 'complete', second.get('failure'))
        self.assertEqual(second['counts']['applied'], 0)
        self.assertEqual([r['id'] for r in state.catalog_item(PUBLISHED)['representations']], [READ])
        self.assertEqual(state.catalog_item(PUBLISHED)['title'], 'Round trip zine')
        index = state.print_index(PUBLISHED, READ)
        self.assertEqual((index['pages'], index['sections']), (2, {'1': 'Round trip page one', '2': 'Round trip page two'}))


if __name__ == '__main__':
    unittest.main()
