import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from prepare_print_migration import prepare


class PrintMigrationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        archive = self.root / 'archive'
        (archive / 'snapshots').mkdir(parents=True)
        pdf = b'%PDF synthetic converter fixture'
        asset = hashlib.sha256(pdf).hexdigest()
        path = 'originals/' + asset[:2] + '/' + asset
        (archive / path).parent.mkdir(parents=True)
        (archive / path).write_bytes(pdf)
        source = {'metadata': {'identifier': 'retained-source', 'future': {'keep': True}},
                  'files': [{'name': 'edition.pdf', 'size': str(len(pdf)), 'unknown': 'retained'}]}
        old = hashlib.sha256(json.dumps(source).encode()).hexdigest()
        raw = json.dumps(source, indent=2).encode()
        snapshot_path = 'snapshots/' + old + '.json'
        (archive / snapshot_path).write_bytes(raw)
        dump = b'retained legacy dump fixture'
        (self.root / 'catalog.dump').write_bytes(dump)
        self.catalog = {
            'assets': [{'sha256': asset, 'size': len(pdf)}],
            'collections': [{'id': 'zines', 'config': {'limits': {'total_bytes': 1024}}}],
            'documents': [{'id': 'd' * 64, 'collection_id': 'zines', 'source_item': 'retained-source',
                           'metadata': {'title': 'Source title', 'date': 'unknown'},
                           'overrides': {'title': 'Reviewed title'}, 'published': True,
                           'rights': 'Private only', 'review_note': 'Retained private decision'}],
            'variants': [{'id': 'e' * 64, 'document_id': 'd' * 64, 'filename': 'edition.pdf',
                          'asset_sha': asset, 'source_snapshot': old, 'kind': 'original'}],
            'source_snapshots': [{'id': old, 'item_id': 'retained-source', 'raw': source}],
            'asset_companions': [],
        }
        self.catalog_file = self.root / 'export.json'
        self.restore_file = self.root / 'restore.json'
        self.catalog_file.write_text(json.dumps(self.catalog))
        dump_hash = hashlib.sha256(dump).hexdigest()
        (self.root / 'complete.json').write_text(json.dumps({'complete': True, 'catalog_sha256': dump_hash,
            'catalog_size': len(dump), 'files': {path: asset, snapshot_path: hashlib.sha256(raw).hexdigest()}}))
        self.restore_file.write_text(json.dumps({'restore_exit_code': 0, 'dump_sha256': dump_hash,
            'table_counts': {table: len(rows) for table, rows in self.catalog.items()}}))

    def run_prepare(self):
        return prepare(self.root, self.catalog_file, self.restore_file, self.root / 'output')

    def test_preserves_legacy_identity_and_binds_exact_retained_serialization(self):
        plan = self.run_prepare()
        source = plan['sources'][0]
        self.assertNotEqual(source['sha256'], source['legacy_snapshot_id'])
        item = json.loads((self.root / 'output' / ('item-' + 'd' * 64 + '.json')).read_text())
        self.assertEqual(item['title'], 'Reviewed title')
        self.assertEqual(item['editorial']['legacy_document']['metadata']['title'], 'Source title')
        self.assertEqual(item['representations'][0]['snapshot_sha256'], source['sha256'])
        self.assertEqual(item['editorial']['legacy_variants'][0]['source_snapshot'], source['legacy_snapshot_id'])

    def test_records_each_collection_legacy_limits(self):
        self.catalog['collections'].append({'id': 'a-info-zines', 'config': {
            'limits': {'total_bytes': 157286400}, 'updates': {'minimum_free_bytes': 4 << 30}}})
        self.catalog_file.write_text(json.dumps(self.catalog))
        restore = json.loads(self.restore_file.read_text())
        restore['table_counts']['collections'] = 2
        self.restore_file.write_text(json.dumps(restore))
        plan = self.run_prepare()
        self.assertEqual({c: (v['budget'], v['reserve']) for c, v in plan['collection_limits'].items()},
                         {'zines': (1024, 2 << 30), 'a-info-zines': (157286400, 4 << 30)})
        saved = json.loads((self.root / 'output/migration-plan.json').read_text())
        self.assertEqual(saved['collection_limits'], plan['collection_limits'])

    def test_collection_without_legacy_limits_is_rejected(self):
        self.catalog['collections'][0]['config'] = {}
        self.catalog_file.write_text(json.dumps(self.catalog))
        with self.assertRaisesRegex(ValueError, 'limits.total_bytes'):
            self.run_prepare()
        self.assertFalse((self.root / 'output').exists())

    def test_database_only_unreferenced_history_is_retained_without_fabricated_bytes(self):
        self.catalog['source_snapshots'].append({'id': 'f' * 64, 'item_id': 'retained-source', 'raw': {'history': True}})
        self.catalog_file.write_text(json.dumps(self.catalog))
        restore = json.loads(self.restore_file.read_text())
        restore['table_counts']['source_snapshots'] = 2
        self.restore_file.write_text(json.dumps(restore))
        plan = self.run_prepare()
        self.assertEqual(plan['database_only_historical_snapshots'], ['f' * 64])
        self.assertEqual(len(plan['sources']), 1)
        retained = json.loads((self.root / 'output/retained-export-zines.json').read_text())
        self.assertEqual(retained['tables']['source_snapshots'], self.catalog['source_snapshots'])

    def test_missing_active_snapshot_still_rejects_migration(self):
        for path in (self.root / 'archive/snapshots').iterdir():
            path.unlink()
        receipt = json.loads((self.root / 'complete.json').read_text())
        receipt['files'] = {key: value for key, value in receipt['files'].items() if not key.startswith('snapshots/')}
        (self.root / 'complete.json').write_text(json.dumps(receipt))
        with self.assertRaises(FileNotFoundError):
            self.run_prepare()
        self.assertFalse((self.root / 'output').exists())

    def test_unpublished_listing_does_not_gain_transport_or_public_access(self):
        self.catalog['documents'][0]['published'] = False
        self.catalog_file.write_text(json.dumps(self.catalog))
        plan = self.run_prepare()
        self.assertEqual(plan['reviews'], [])
        self.assertEqual(len(list((self.root / 'output').glob('item-*.json'))), 1)
        self.assertEqual(list((self.root / 'output').glob('review-*.json')), [])

    def test_public_retention_requires_existing_variant_approval(self):
        self.catalog['variants'][0]['public_approved'] = True
        self.catalog_file.write_text(json.dumps(self.catalog))
        prepare(self.root, self.catalog_file, self.restore_file, self.root / 'output', retain_public=True)
        review = json.loads(next((self.root / 'output').glob('review-*.json')).read_text())['review']
        self.assertTrue(review['public'])
        self.assertTrue(review['read'])
        self.assertFalse(review['acquisition'])
        self.assertFalse(review['relay'])
        retained = json.loads((self.root / 'output/retained-export-zines.json').read_text())
        self.assertEqual(retained['tables'], self.catalog)
        self.assertEqual(retained['counts']['variants'], 1)

    def test_corrupt_independent_original_rejected_before_output_creation(self):
        original = next((self.root / 'archive/originals').rglob('*' + self.catalog['assets'][0]['sha256']))
        original.write_bytes(b'changed bytes')
        with self.assertRaisesRegex(ValueError, 'Archive differs'):
            self.run_prepare()
        self.assertFalse((self.root / 'output').exists())

    def test_recovered_page_text_and_cover_keep_exact_identity(self):
        asset = self.catalog['assets'][0]['sha256']
        cover = 'derivatives/' + asset + '/cover.png'
        path = self.root / 'archive' / cover
        path.parent.mkdir(parents=True)
        path.write_bytes(b'verified synthetic cover')
        receipt_path = self.root / 'complete.json'
        receipt = json.loads(receipt_path.read_text())
        receipt['files'][cover] = hashlib.sha256(path.read_bytes()).hexdigest()
        receipt_path.write_text(json.dumps(receipt))
        self.catalog['derivatives'] = [{'asset_sha': asset, 'status': 'page-text', 'pages': 2, 'tool': 'retained tool'}]
        self.catalog['page_text'] = [{'asset_sha': asset, 'page': 2, 'text': 'Exact original page two'}]
        self.catalog_file.write_text(json.dumps(self.catalog))
        restore = json.loads(self.restore_file.read_text())
        restore['table_counts'].update(derivatives=1, page_text=1)
        self.restore_file.write_text(json.dumps(restore))
        plan = self.run_prepare()
        self.assertEqual(len(plan['indexes']), 1)
        index = json.loads(next((self.root / 'output').glob('index-*.json')).read_text())
        self.assertEqual(index['sections'], {'2': 'Exact original page two'})
        self.assertEqual(index['cover_sha256'], receipt['files'][cover])
        self.assertEqual(index['original_sha256'], asset)


if __name__ == '__main__':
    unittest.main()
