"""Prepare private audio migration inputs from an independently restored Dedhed donor.

The donor is either Dedhed's private backup or its public checkpoint plus a
directory of retained original track files.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re


def digest(path):
    value = hashlib.sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            value.update(chunk)
    return value.hexdigest()


def encode(value):
    # Match Go's struct JSON representation for the ordered original manifest.
    return json.dumps(value, ensure_ascii=False, separators=(',', ':')).replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029').encode()


def checked_receipt(data, recovered, collection):
    if not recovered.get('independent_host') or not recovered.get('restored_on') or recovered['independent_host'] == recovered['restored_on']:
        raise ValueError('Independent backup and restore evidence required')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,199}', collection):
        raise ValueError('Invalid collection identity')
    if set(data) != set(recovered['counts']):
        raise ValueError('Complete restored table export required')
    for table, rows in data.items():
        if recovered['counts'].get(table) != len(rows):
            raise ValueError('Restored table count differs: ' + table)


class PrivateBackup:
    """Dedhed's private backup: dump, retained sources, originals, media and acquisition receipts."""

    def __init__(self, backup, recovered):
        self.root = Path(backup)
        manifest = json.loads((self.root / 'manifest.json').read_text())
        if recovered.get('files_verified') != len(manifest) or recovered.get('dump_sha256') != manifest.get('catalog.dump'):
            raise ValueError('Restore and backup manifests differ')
        for name, expected in manifest.items():
            file = self.root / name
            if file.is_symlink() or not file.resolve().is_relative_to(self.root.resolve()) or digest(file) != expected:
                raise ValueError('Retained backup file differs: ' + name)
        self.description = {'kind': 'private-backup', 'dump_sha256': manifest['catalog.dump']}

    def retained_source(self, identity):
        return (self.root / 'data/sources' / (identity + '.json')).read_bytes()

    def playback(self, row, recording, raw):
        identity = row['id']
        acquisition = json.loads((self.root / 'data' / (identity + '.acquisition.json')).read_text())
        originals = []
        files = {file['source_name']: file for file in acquisition['files']}
        for track in recording['tracks']:
            original = files[track['file']['name']]
            name = Path(original['path']).name
            path = self.root / 'data/originals' / name
            if digest(path) != original['sha256'] or path.stat().st_size != original['bytes'] or original['bytes'] != track['file']['bytes']:
                raise ValueError('Original recording input differs')
            originals.append({'file': original['source_name'], 'bytes': original['bytes'], 'sha256': original['sha256'], 'path': name})
        derived = self.root / 'data/media' / row['media']
        if derived.name != row['media'] or digest(derived) != row['media_sha256']:
            raise ValueError('Continuous recording differs')
        return originals, derived, 'Retained Dedhed acquisition receipt: ' + acquisition['ffmpeg_version']


def safe_name(name):
    if not isinstance(name, str) or not name or Path(name).name != name or name in ('.', '..') or '\\' in name or '\x00' in name:
        raise ValueError('Unsafe file name')
    return name


class PublicCheckpoint:
    """Dedhed's public checkpoint: dump, manifest and continuous files, plus retained original tracks.

    The checkpoint holds no acquisition receipts, so each original is found by
    the byte count and checksums the restored database recorded for its track.
    """

    def __init__(self, checkpoint, media, originals, recovered):
        checkpoint, media, self.originals = Path(checkpoint), Path(media), Path(originals)
        manifest = json.loads((checkpoint / 'manifest.json').read_text())
        dump = checkpoint / 'database.dump'
        if (dump.is_symlink() or recovered.get('dump_sha256') != manifest.get('dump_sha256')
                or dump.stat().st_size != manifest.get('dump_bytes') or digest(dump) != manifest['dump_sha256']):
            raise ValueError('Restore and checkpoint manifests differ')
        self.media = {}
        for entry in manifest.get('media', []):
            path = media / safe_name(entry['name'])
            if path.is_symlink() or not path.is_file() or path.stat().st_size != entry['bytes'] or digest(path) != entry['sha256']:
                raise ValueError('Checkpoint continuous file differs: ' + entry['name'])
            self.media[entry['sha256']] = path
        self.files = []
        for path in sorted(self.originals.iterdir()):
            if path.is_symlink() or not path.is_file():
                raise ValueError('Original track directory must hold only regular files: ' + path.name)
            self.files.append(path)
        self.hashes = {}
        self.tool = 'Retained Dedhed public checkpoint ' + checkpoint.name + ' continuous file; its extraction tool is not recorded there'
        self.description = {'kind': 'public-checkpoint', 'checkpoint': checkpoint.name, 'observed_at': manifest.get('observed_at'),
                            'revision': manifest.get('revision'), 'dump_sha256': manifest['dump_sha256'],
                            'media': manifest.get('media', [])}

    def retained_source(self, identity):
        return None

    def checksums(self, path):
        if path not in self.hashes:
            values = {name: hashlib.new(name) for name in ['md5', 'sha1', 'sha256']}
            with path.open('rb') as source:
                for chunk in iter(lambda: source.read(1024 * 1024), b''):
                    for value in values.values():
                        value.update(chunk)
            self.hashes[path] = {name: value.hexdigest() for name, value in values.items()}
        return self.hashes[path]

    def original(self, file, raw):
        name, size = safe_name(file['name']), file['bytes']
        md5 = str(file.get('md5', '')).lower()
        if not re.fullmatch(r'[0-9a-f]{32}', md5) or not isinstance(size, int) or size <= 0:
            raise ValueError('Restored database records no size and checksum for original: ' + name)
        expected = {'md5': md5}
        listed = next((entry for entry in raw.get('files', []) if entry.get('name') == name), {}) if isinstance(raw, dict) else {}
        if listed.get('sha1'):
            expected['sha1'] = str(listed['sha1']).lower()
        if (listed.get('md5') and str(listed['md5']).lower() != md5) or (listed.get('size') and str(listed['size']) != str(size)):
            raise ValueError('Restored source snapshot and playback record differ for original: ' + name)
        matches = {}
        for path in self.files:
            if path.stat().st_size == size:
                found = self.checksums(path)
                if all(found[key] == value for key, value in expected.items()):
                    matches[found['sha256']] = path
        if len(matches) != 1:
            raise ValueError('No single retained original matches the restored checksums: ' + name)
        sha256, path = matches.popitem()
        return {'file': name, 'bytes': size, 'sha256': sha256, 'path': path.name}

    def playback(self, row, recording, raw):
        derived = self.media.get(row['media_sha256'])
        if derived is None:
            return None
        if derived.name != row['media']:
            raise ValueError('Continuous recording differs')
        return [self.original(track['file'], raw) for track in recording['tracks']], derived, self.tool


def legacy_schedule(rows, recordings, scheduled):
    """Record Dedhed's station timeline unchanged for the apply step to convert."""
    if not rows:
        return None
    if len(rows) != 1 or not isinstance(rows[0].get('schedule'), dict):
        raise ValueError('Invalid legacy station schedule')
    schedule = rows[0]['schedule']
    programs = schedule.get('programs')
    if not schedule.get('revision') or not isinstance(programs, list):
        raise ValueError('Invalid legacy station schedule')
    referenced = sorted({program.get('recording_id') for program in programs})
    if any(identity not in recordings for identity in referenced):
        raise ValueError('Legacy station schedule references an unknown recording')
    return {'revision': schedule['revision'], 'updated_at': rows[0].get('updated_at'), 'programs': programs,
            'recordings': referenced, 'not_schedule_eligible': [identity for identity in referenced if identity not in scheduled]}


def prepare(backup, export, receipt, output, collection, retain_public=False):
    """Prepare from Dedhed's private backup layout."""
    data, recovered = json.loads(Path(export).read_text()), json.loads(Path(receipt).read_text())
    checked_receipt(data, recovered, collection)
    return build(PrivateBackup(backup, recovered), data, receipt, output, collection, retain_public)


def prepare_checkpoint(checkpoint, media, originals, export, receipt, output, collection, retain_public=False):
    """Prepare from Dedhed's public checkpoint and a directory of retained original tracks."""
    data, recovered = json.loads(Path(export).read_text()), json.loads(Path(receipt).read_text())
    checked_receipt(data, recovered, collection)
    return build(PublicCheckpoint(checkpoint, media, originals, recovered), data, receipt, output, collection, retain_public)


def build(donor, data, receipt, output, collection, retain_public):
    output = Path(output)
    recovery = digest(Path(receipt))
    snapshots = {row['sha256']: row for row in data['source_snapshots']}
    sources, inputs, mappings = [], {}, []
    approved = 0
    public_count = 0
    public_approvals = {row['recording_id']: row for row in data.get('public_approvals', [])}
    scheduled = set()
    withheld = []
    for row in data['recordings']:
        identity = row['id']
        if not identity or '/' in identity or '\\' in identity or '..' in identity:
            raise ValueError('Unsafe recording identity')
        snapshot = snapshots[row['source_hash']]
        if snapshot['item_id'] != identity:
            raise ValueError('Snapshot recording identity differs')
        retained_bytes = donor.retained_source(identity)
        retained_hash = hashlib.sha256(retained_bytes).hexdigest() if retained_bytes is not None else None
        equal = json.loads(retained_bytes) == snapshot['raw'] if retained_bytes is not None else None
        raw = retained_bytes if equal else encode(snapshot['raw'])
        source_hash = hashlib.sha256(raw).hexdigest()
        observations = [(retained_bytes, 'retained-file')] if retained_bytes is not None else []
        for value, source in observations + [(raw, 'restored-database-json')]:
            sha = hashlib.sha256(value).hexdigest()
            name = 'source-' + sha + '.json'
            if name not in inputs:
                inputs[name] = value
                sources.append({'item_id': identity, 'file': name, 'sha256': sha, 'origin': source})
        mappings.append({'id': identity, 'legacy_snapshot_sha256': row['source_hash'], 'retained_file_sha256': retained_hash,
                         'catalog_snapshot_sha256': source_hash, 'retained_file_matches_database': equal})
        recording = copy.deepcopy(row['playback'] or row['source'])
        # Match the donor's effective catalog view without rewriting source rows.
        for key in ['kind', 'title', 'concert_id']:
            if row['review'].get(key):
                recording[key] = row['review'][key]
        if row['review'].get('sets'):
            recording['sets'] = copy.deepcopy(row['review']['sets'])
        if recording['id'] != identity or not recording['tracks']:
            raise ValueError('Recording identity or tracks missing')
        tracks = []
        for index, track in enumerate(recording['tracks']):
            section = next((s for s in recording['sets'] if s['reviewed'] and s['first'] <= index <= s['last']), None)
            mapped = {'id': 'track-' + str(index), 'file': track['file']['name'], 'title': track['title'], 'set_reviewed': bool(section)}
            if section:
                mapped['set'] = section['title']
            tracks.append(mapped)
        representation = {'id': identity, 'media': 'audio', 'source_item_id': identity, 'snapshot_sha256': source_hash,
                          'file': tracks[0]['file'], 'collections': [collection], 'extension': {'audio': {
                              'recording_id': identity, 'concert_id': recording.get('concert_id', ''), 'tracks': tracks,
                              'recording': recording, 'credits': {key: recording.get(key, '') for key in ['source', 'taper', 'transferer']}}}}
        item = {'id': identity, 'title': recording['title'], 'creator': recording['artist'], 'date': recording['date'],
                'description': recording.get('source', ''), 'collections': [collection], 'representations': [representation],
                'editorial': {'legacy_record': row, 'legacy_snapshot_mapping': mappings[-1], 'migration_scope': 'retained current decisions; public approval imported separately'}}
        inputs['item-' + identity + '.json'] = encode(item)
        review = row['review']
        if not (review.get('published') and review.get('playback') and row['playback'] and row['media']):
            continue
        prepared = donor.playback(row, recording, snapshot['raw'])
        prior_public = public_approvals.get(identity, {})
        if prepared is None:
            if (prior_public.get('approved') and prior_public.get('source_hash') == row['source_hash']
                    and prior_public.get('media_sha256') == row['media_sha256']):
                raise ValueError('Public checkpoint lacks the continuous file of a public recording: ' + identity)
            withheld.append(identity)
            continue
        originals, derived, tool = prepared
        public = False
        if retain_public and prior_public.get('approved'):
            if (prior_public.get('source_hash') != row['source_hash']
                    or prior_public.get('media_sha256') != row['media_sha256']
                    or review.get('kind') != 'audience' or row['source'].get('synthetic')
                    or not prior_public.get('policy_basis') or not prior_public.get('reviewer')):
                raise ValueError('Retained public approval does not match current reviewed audience source/audio')
            public = True
            public_count += 1
        relay = bool(review.get('relay') or public)
        if relay:
            scheduled.add(identity)
        access = {'snapshot_sha256': source_hash, 'files': [track['file'] for track in tracks], 'read': False, 'playback': True,
                  'download': False, 'acquisition': False, 'relay': relay, 'schedule': relay, 'public': public,
                  'notes': review['notes'] + (' Retained public approval: ' + prior_public['policy_basis'] + '; reviewer: ' + prior_public['reviewer'] if public else ' Retained private playback and existing relay decision.')}
        preservation = {'snapshot_sha256': source_hash, 'manifest': originals, 'independent_recovery_sha256': recovery}
        inputs['review-' + identity + '.json'] = encode({'review': access, 'preservation': preservation})
        asset = {'snapshot_sha256': source_hash, 'manifest_sha256': hashlib.sha256(encode(originals)).hexdigest(),
                 'path': row['media'], 'bytes': derived.stat().st_size, 'sha256': row['media_sha256'],
                 'duration': recording['duration'], 'independent_recovery_sha256': recovery,
                 'tool': tool}
        inputs['audio-' + identity + '.json'] = encode(asset)
        approved += 1
    plan = {'collection': collection, 'recordings': len(data['recordings']), 'private_playback_recordings': approved,
            'donor': donor.description, 'sources': sources, 'snapshot_mappings': mappings,
            'legacy_schedule': legacy_schedule(data['station_schedule'], {row['id'] for row in data['recordings']}, scheduled),
            'playback_without_checkpoint_media': withheld,
            'independent_recovery_sha256': recovery, 'new_acquisition': False, 'public': public_count > 0, 'public_playback_recordings': public_count, 'schedule_eligible': sorted(scheduled)}
    inputs['migration-plan.json'] = encode(plan)
    retained_export = {'collection': collection, 'module': 'audio', 'independent_recovery_sha256': recovery,
                       'tables': data, 'counts': {table: len(rows) for table, rows in data.items()}}
    inputs['retained-export.json'] = encode(retained_export)
    output.mkdir(mode=0o700, exist_ok=False)
    for name, value in inputs.items():
        (output / name).write_bytes(value)
        (output / name).chmod(0o400)
    return plan


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    donor = parser.add_mutually_exclusive_group(required=True)
    donor.add_argument('--backup', help='Dedhed private backup directory')
    donor.add_argument('--checkpoint', help='Dedhed public checkpoint directory holding database.dump and manifest.json')
    parser.add_argument('--media', help='Directory holding the public checkpoint continuous files')
    parser.add_argument('--originals', help='Directory of retained original track files, for a public checkpoint')
    for name in ['export', 'receipt', 'output', 'collection']:
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--retain-public-approvals', action='store_true', help='Preserve exact current hash-bound audience approvals from the independently restored donor')
    args = parser.parse_args()
    if args.checkpoint and not (args.media and args.originals):
        parser.error('--checkpoint requires --media and --originals')
    if args.backup and (args.media or args.originals):
        parser.error('--media and --originals apply only to --checkpoint')
    if args.checkpoint:
        plan = prepare_checkpoint(args.checkpoint, args.media, args.originals, args.export, args.receipt, args.output,
                                  args.collection, args.retain_public_approvals)
    else:
        plan = prepare(args.backup, args.export, args.receipt, args.output, args.collection, args.retain_public_approvals)
    print(json.dumps({key: plan[key] for key in ['recordings', 'private_playback_recordings', 'new_acquisition', 'public']}))


if __name__ == '__main__':
    main()
