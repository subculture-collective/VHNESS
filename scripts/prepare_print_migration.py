#!/usr/bin/env python3
"""Prepare private catalog inputs from an independently restored print archive.

Never changes an archive or database. The retained legacy dump owns original
history; mapped items retain document/variant IDs and complete editorial rows.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re


def digest(data):
    return hashlib.sha256(data).hexdigest()


def sha(value):
    if not isinstance(value, str) or not re.fullmatch(r'[0-9a-f]{64}', value):
        raise ValueError('Invalid content identity')
    return value


# The legacy app kept 2 GiB free when acquiring a collection, and used the same
# floor for updates unless updates.minimum_free_bytes set another.
LEGACY_RESERVE = 2 << 30


def legacy_limits(config):
    """The core byte budget and free-space reserve that carry a legacy collection's limits."""
    budget = (config.get('limits') or {}).get('total_bytes')
    reserve = (config.get('updates') or {}).get('minimum_free_bytes') or LEGACY_RESERVE
    for value in (budget, reserve):
        if not isinstance(value, int) or isinstance(value, bool) or value <= 0:
            raise ValueError('Retained collection configuration has no positive limits.total_bytes or minimum_free_bytes')
    return {'budget': budget, 'reserve': reserve,
            'basis': 'legacy limits.total_bytes; updates.minimum_free_bytes, or the legacy 2 GiB free-space floor'}


def filename(value):
    if not value or Path(value).name != value or '\\' in value or '\x00' in value:
        raise ValueError('Invalid source filename')
    return value


def prepare(backup, catalog_file, restore_file, output, retain_public=False):
    receipt = json.loads((backup / 'complete.json').read_text())
    restore = json.loads(restore_file.read_text())
    catalog = json.loads(catalog_file.read_text())
    archive = backup / 'archive'
    dump = (backup / 'catalog.dump').read_bytes()
    if (receipt.get('complete') is not True or restore.get('restore_exit_code') != 0
            or restore.get('dump_sha256') != receipt['catalog_sha256']
            or digest(dump) != receipt['catalog_sha256'] or len(dump) != receipt['catalog_size']):
        raise ValueError('Independent dump/restore evidence does not match')
    for name, expected in receipt['files'].items():
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError('Invalid recovery path')
        file = archive / relative
        if file.is_symlink() or digest(file.read_bytes()) != expected:
            raise ValueError('Archive differs from the independent backup: ' + name)
    for table, rows in catalog.items():
        if not isinstance(rows, list) or restore['table_counts'].get(table) != len(rows):
            raise ValueError('Restored table coverage differs: ' + table)
    assets = {sha(row['sha256']): row for row in catalog['assets']}
    snapshots = {}
    required_snapshots = {row['source_snapshot'] for row in catalog['variants']}
    required_snapshots.update(row['source_snapshot'] for row in catalog['asset_companions'])
    historical_snapshots = []
    for row in catalog['source_snapshots']:
        key = sha(row['id'])
        snapshot_file = archive / 'snapshots' / (key + '.json')
        if not snapshot_file.exists() and key not in required_snapshots:
            # Unreferenced audit refreshes can exist only in the donor database.
            # Keep their full rows in the immutable export; never invent raw bytes.
            historical_snapshots.append(key)
            continue
        raw = snapshot_file.read_bytes()
        # Legacy snapshot IDs can identify a prior JSON serialization. Bind the
        # shared source to the exact independently retained file bytes, while
        # preserving the old identity and checking complete semantic equality.
        if json.loads(raw) != row['raw']:
            raise ValueError('Original source response differs from restored metadata')
        snapshots[key] = (row['item_id'], raw, digest(raw))
    collections = {row['id']: row['config'] for row in catalog['collections']}
    limits = {collection: legacy_limits(config) for collection, config in collections.items()}
    planned = []
    reviewed = []
    indexes = []
    derivatives = {row['asset_sha']: row for row in catalog.get('derivatives', [])}
    used_variants = set()
    recovery = digest(restore_file.read_bytes())

    def original(name, key):
        filename(name)
        key = sha(key)
        relative = 'originals/' + key[:2] + '/' + key
        file = archive / relative
        if key not in assets or file.is_symlink() or file.stat().st_size != assets[key]['size'] or digest(file.read_bytes()) != key:
            raise ValueError('Unverified preserved original: ' + name)
        return {'file': name, 'bytes': assets[key]['size'], 'sha256': key, 'path': relative}

    for document in catalog['documents']:
        sha(document['id'])
        collection = document['collection_id']
        if collection not in collections:
            raise ValueError('Document has no retained collection configuration')
        metadata = {**document['metadata'], **document['overrides']}
        legacy_variants = [v for v in catalog['variants'] if v['document_id'] == document['id']]
        item = {'id': document['id'], 'title': metadata['title'], 'description': document['rights'],
                'date': metadata.get('date', ''), 'creator': metadata.get('creator', ''),
                'collections': [collection], 'editorial': {'legacy_document': document, 'legacy_variants': legacy_variants}, 'representations': []}
        for variant in catalog['variants']:
            if variant['document_id'] != document['id']:
                continue
            if variant['id'] in used_variants or not variant['filename'].lower().endswith('.pdf'):
                raise ValueError('Duplicate or unsupported legacy variant')
            used_variants.add(variant['id'])
            sha(variant['id'])
            source, raw, bound_snapshot = snapshots[variant['source_snapshot']]
            if source != document['source_item']:
                raise ValueError('Variant source differs from its document')
            manifest = [original(variant['filename'], variant['asset_sha'])]
            companions = []
            for companion in catalog['asset_companions']:
                if companion['asset_sha'] != variant['asset_sha']:
                    continue
                if companion['source_snapshot'] != variant['source_snapshot']:
                    companion_source, companion_raw, _ = snapshots[companion['source_snapshot']]
                    prior = next((f for f in json.loads(companion_raw)['files'] if f['name'] == companion['filename']), None)
                    current = next((f for f in json.loads(raw)['files'] if f['name'] == companion['filename']), None)
                    # A metadata refresh can produce another snapshot identity
                    # while retaining exactly the same OCR object. Require the
                    # same source and complete size/checksum evidence in both.
                    if (companion_source != source or prior is None or current is None
                            or any(not prior.get(k) or prior[k] != current.get(k) for k in ('size', 'sha1', 'md5'))):
                        raise ValueError('OCR companion source differs from its edition')
                    companion_key = sha(companion['companion_sha'])
                    data = (archive / ('originals/' + companion_key[:2] + '/' + companion_key)).read_bytes()
                    if (str(len(data)) != prior['size'] or hashlib.sha1(data).hexdigest() != prior['sha1']
                            or hashlib.md5(data).hexdigest() != prior['md5']):
                        raise ValueError('Refreshed OCR companion bytes differ')
                companions.append({'file': filename(companion['filename']), 'role': 'ocr'})
                manifest.append(original(companion['filename'], companion['companion_sha']))
            inventory = {entry['name'] for entry in json.loads(raw)['files']}
            if any(entry['file'] not in inventory for entry in manifest):
                raise ValueError('Preserved original absent from complete source response')
            item['representations'].append({'id': variant['id'], 'media': 'print', 'source_item_id': source,
                'snapshot_sha256': bound_snapshot, 'file': variant['filename'], 'collections': [collection],
                'extension': {'print': {'document_id': document['id'], 'variant': variant['kind'], 'pages': 0, 'companions': companions}}})
            if document['published']:
                reviewed.append({'item_id': document['id'], 'representation_id': variant['id'], 'collection': collection,
                    'input': {'review': {'snapshot_sha256': bound_snapshot, 'files': [entry['file'] for entry in manifest],
                        'read': True, 'download': True, 'playback': False, 'acquisition': False, 'relay': False,
                        'schedule': False, 'public': bool(retain_public and variant.get('public_approved')), 'notes': ('Retained current public variant approval. ' if retain_public and variant.get('public_approved') else 'Retained private pilot reading/download decision. ') + document['review_note']},
                        'preservation': {'snapshot_sha256': bound_snapshot, 'manifest': manifest,
                                         'independent_recovery_sha256': recovery}}})
                derivative = derivatives.get(variant['asset_sha'])
                if derivative and derivative['status'] in ('page-text', 'document-text', 'no-text'):
                    sections = {}
                    for row in catalog.get('page_text', []):
                        if row['asset_sha'] != variant['asset_sha']:
                            continue
                        page = row['page']
                        if page in sections or not isinstance(page, int) or page < 0 or page > derivative['pages']:
                            raise ValueError('Invalid recovered text page')
                        sections[page] = row['text']
                    if (not 1 <= derivative['pages'] <= 10000 or sum(len(text.encode()) for text in sections.values()) > 16 << 20
                            or (derivative['status'] == 'page-text' and (not sections or 0 in sections))
                            or (derivative['status'] == 'document-text' and set(sections) != {0})
                            or (derivative['status'] == 'no-text' and sections)):
                        raise ValueError('Invalid recovered text coverage')
                    index = {'independent_recovery_sha256': recovery, 'snapshot_sha256': bound_snapshot,
                             'original_sha256': variant['asset_sha'], 'pages': derivative['pages'],
                             'text_status': derivative['status'], 'tool': derivative['tool'], 'sections': sections,
                             'text_file': '', 'text_sha256': '', 'cover_path': '', 'cover_sha256': '', 'cover_bytes': 0}
                    if derivative['status'] == 'document-text':
                        candidates = [entry for entry in manifest[1:] if (archive / entry['path']).read_text() == sections[0]]
                        if len(candidates) != 1:
                            raise ValueError('Recovered document text has no exact preserved OCR companion')
                        index['text_file'], index['text_sha256'] = candidates[0]['file'], candidates[0]['sha256']
                    cover = 'derivatives/' + variant['asset_sha'] + '/cover.png'
                    if cover in receipt['files']:
                        index.update(cover_path=cover, cover_sha256=receipt['files'][cover], cover_bytes=(archive / cover).stat().st_size)
                    indexes.append({'item_id': document['id'], 'representation_id': variant['id'],
                                    'collection': collection, 'input': index})
        if not item['representations']:
            raise ValueError('Document has no mapped representation')
        planned.append(item)
    if len(used_variants) != len(catalog['variants']):
        raise ValueError('Unmapped legacy variants')
    # Only after all validation succeeds, allocate a new output directory.
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    def save(name, value):
        target = output / name
        target.write_text(json.dumps(value, indent=2) + '\n')
        target.chmod(0o600)
    for _, raw, bound_snapshot in snapshots.values():
        target = output / ('source-' + bound_snapshot + '.json')
        target.write_bytes(raw)
        target.chmod(0o600)
    for item in planned:
        save('item-' + sha(item['id']) + '.json', item)
    for review in reviewed:
        save('review-' + sha(review['representation_id']) + '.json', review['input'])
    for index in indexes:
        save('index-' + sha(index['representation_id']) + '.json', index['input'])
    plan = {'legacy_dump_sha256': receipt['catalog_sha256'], 'legacy_export_sha256': digest(catalog_file.read_bytes()),
            'independent_restore_sha256': recovery, 'documents': len(planned), 'variants': len(used_variants),
            'collections': collections, 'sources': [{'item_id': item, 'legacy_snapshot_id': key, 'sha256': bound_snapshot}
                for key, (item, _, bound_snapshot) in snapshots.items()],
            'reviews': [{k: v for k, v in review.items() if k != 'input'} for review in reviewed],
            'indexes': [{k: v for k, v in index.items() if k != 'input'} for index in indexes],
            'database_only_historical_snapshots': historical_snapshots,
            'history_owner': 'Retained original dump and independently restored legacy database',
            'scope': ('Retained current published variant approvals only; no new public scope' if retain_public else 'Private existing pilot only; no acquisition, relay or public approval')}
    for collection in collections:
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,199}', collection):
            raise ValueError('Invalid retained collection identity')
        save('retained-export-' + collection + '.json', {'collection': collection, 'module': 'print',
             'independent_recovery_sha256': recovery, 'tables': catalog,
             'counts': {table: len(rows) for table, rows in catalog.items()}})
    plan['collection_limits'] = limits
    save('migration-plan.json', plan)
    return plan


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backup', required=True, type=Path)
    parser.add_argument('--restored-catalog', required=True, type=Path)
    parser.add_argument('--restore-receipt', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--retain-public-approvals', action='store_true', help='Preserve only current published/approved variants in the independently restored donor; never approve additional variants')
    args = parser.parse_args()
    plan = prepare(args.backup, args.restored_catalog, args.restore_receipt, args.output, args.retain_public_approvals)
    print('Private migration inputs:', plan['documents'], 'documents,', plan['variants'], 'variants')
