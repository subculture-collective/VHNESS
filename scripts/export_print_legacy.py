#!/usr/bin/env python3
"""Export a restored legacy print pilot into the inputs prepare_print_migration.py reads.

Reads every legacy table from a restored database through psql, in one read-only
statement, and hashes every file under BACKUP/archive and BACKUP/catalog.dump.
Writes a new directory holding the backup manifest (complete.json), the table
export (restored-catalog.json) and the restore receipt (restore-receipt.json),
each mode 0600. Changes no database, dump or archive.

--source records what the manifest describes: `backup` for an independent
off-host backup copy, the only evidence prepare accepts for a cutover, or `live`
for the live archive and a fresh dump of the live database (rehearsals only).
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shlex

from apply_print_migration import ApplyError, PsqlState, libpq_env

# Every table of the legacy print schema. A database with any other table stops
# the export, so nothing is left out silently.
TABLES = ('acquisition_attempts', 'app_metadata', 'asset_companions', 'assets', 'collections', 'derivatives',
          'documents', 'page_text', 'public_access_events', 'review_events', 'schema_versions', 'source_changes',
          'source_discoveries', 'source_inventories', 'source_refresh_runs', 'source_snapshots', 'variants')
# page_text.search is a generated tsvector; a restore rebuilds it from the text.
PAGE_TEXT_COLUMNS = ('asset_sha', 'page', 'text')

SOURCE_NOTES = {
    'backup': 'The operator states that the archive and dump are an independent off-host backup copy.',
    'live': 'Describes the live archive and a dump of the live database, not an independent off-host '
            'backup. Use it for rehearsals only; prepare refuses it unless --allow-live-source is passed.',
}

SCHEMA_SQL = """SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY;
SELECT json_build_object(
 'tables',(SELECT COALESCE(json_agg(table_name::text ORDER BY table_name),'[]') FROM information_schema.tables
           WHERE table_schema=current_schema() AND table_type='BASE TABLE'),
 'page_text_columns',(SELECT COALESCE(json_agg(column_name::text ORDER BY column_name),'[]') FROM information_schema.columns
           WHERE table_schema=current_schema() AND table_name='page_text' AND is_generated='NEVER'));
"""


def export_sql():
    """One statement, so the counts and rows come from the same snapshot."""
    counts = ','.join("'%s',(SELECT count(*) FROM %s)" % (t, t) for t in TABLES)
    rows = []
    for t in TABLES:
        if t == 'page_text':
            row = 'json_build_object(%s)' % ','.join("'%s',t.%s" % (c, c) for c in PAGE_TEXT_COLUMNS)
            order = 't.asset_sha,t.page'
        else:
            row, order = 't', 't::text'
        rows.append("'%s',(SELECT COALESCE(json_agg(%s ORDER BY %s),'[]'::json) FROM %s t)" % (t, row, order, t))
    return ('SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY;\n'
            "SELECT json_build_object('counts',json_build_object(%s),'tables',json_build_object(%s));\n"
            % (counts, ','.join(rows)))


def file_sha256(path):
    h = hashlib.sha256()
    with open(path, 'rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def archive_files(archive):
    """Hash every regular file under archive. Links and special files stop the export."""
    if not archive.is_dir():
        raise ValueError('Archive root is not a directory: ' + str(archive))
    files, total = {}, 0
    for root, dirs, names in os.walk(archive):
        dirs.sort()
        for name in sorted(dirs + names):
            path = Path(root) / name
            relative = str(path.relative_to(archive))
            if path.is_symlink() or not (path.is_dir() or path.is_file()):
                raise ValueError('Archive holds a link or special file: ' + relative)
            if path.is_file():
                files[relative] = file_sha256(path)
                total += path.stat().st_size
    return files, total


def export(backup, output, source, restore_exit_code, query, now=None):
    """Write complete.json, restored-catalog.json and restore-receipt.json to a new output directory.

    `query(sql)` runs one read-only statement on the restored database and returns its JSON value."""
    now = now or (lambda: datetime.now(timezone.utc).isoformat(timespec='seconds'))
    if source not in SOURCE_NOTES:
        raise ValueError('Source must be backup or live')
    if restore_exit_code != 0:
        raise ValueError('The restore failed (exit code %d); export only a complete restore' % restore_exit_code)
    if output.exists():
        raise ValueError('Output already exists: ' + str(output))
    dump = backup / 'catalog.dump'
    if dump.is_symlink() or not dump.is_file() or dump.stat().st_size == 0:
        raise ValueError('Backup has no catalog.dump file')
    schema = query(SCHEMA_SQL)
    missing = sorted(set(TABLES) - set(schema['tables']))
    unknown = sorted(set(schema['tables']) - set(TABLES))
    if missing or unknown:
        raise ValueError('Restored database is not the legacy print schema; missing: %s; unknown: %s'
                         % (', '.join(missing) or 'none', ', '.join(unknown) or 'none'))
    if sorted(schema['page_text_columns']) != sorted(PAGE_TEXT_COLUMNS):
        raise ValueError('page_text columns differ from the legacy schema: ' + ', '.join(schema['page_text_columns']))
    result = query(export_sql())
    counts, tables = result['counts'], result['tables']
    for table in TABLES:
        if counts[table] != len(tables[table]):
            raise ValueError('Exported rows differ from the table count: ' + table)
    files, total = archive_files(backup / 'archive')
    dump_sha = file_sha256(dump)
    catalog = (json.dumps(tables, indent=1, sort_keys=True) + '\n').encode()
    created = now()
    manifest = {'complete': True, 'source': source, 'source_note': SOURCE_NOTES[source],
                'catalog_sha256': dump_sha, 'catalog_size': dump.stat().st_size,
                'file_count': len(files), 'file_bytes': total, 'files': files,
                'created_at': created, 'tool': 'scripts/export_print_legacy.py'}
    # No timestamp: prepared inputs bind this receipt's hash, so exporting the same
    # restored database twice yields the same inputs.
    receipt = {'restore_exit_code': restore_exit_code, 'dump_sha256': dump_sha, 'table_counts': counts,
               'source': source, 'restored_catalog_sha256': hashlib.sha256(catalog).hexdigest(),
               'tool': 'scripts/export_print_legacy.py'}
    # Only after every read and check succeeds, allocate a new output directory.
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    written = {}
    for name, data in (('restored-catalog.json', catalog),
                       ('restore-receipt.json', (json.dumps(receipt, indent=1) + '\n').encode()),
                       ('complete.json', (json.dumps(manifest, indent=1) + '\n').encode())):
        target = output / name
        with os.fdopen(os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'wb') as handle:
            handle.write(data)
        os.chmod(target, 0o600)  # a umask can have removed bits from the creation mode
        written[name] = hashlib.sha256(data).hexdigest()
    return {'source': source, 'files': len(files), 'bytes': total, 'dump_sha256': dump_sha,
            'rows': sum(counts.values()), 'outputs': written}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--backup', required=True, type=Path,
                        help='Directory holding catalog.dump (the dump that was restored) and archive/ (the archive root)')
    parser.add_argument('--output', required=True, type=Path, help='New directory for the three outputs')
    parser.add_argument('--source', required=True, choices=sorted(SOURCE_NOTES),
                        help='backup: an independent off-host backup copy; live: the live archive (rehearsals only)')
    parser.add_argument('--restore-exit-code', required=True, type=int,
                        help='Exit code of the pg_restore that loaded catalog.dump into the database being read')
    parser.add_argument('--psql', default='psql', help='psql command prefix for the restored database')
    args = parser.parse_args()
    try:
        env = libpq_env(os.environ)
    except (ApplyError, OSError) as error:
        raise SystemExit('Stopped before reading: ' + str(error))
    state = PsqlState(shlex.split(args.psql), env)
    try:
        result = export(args.backup, args.output, args.source, args.restore_exit_code, state.query)
    except (ApplyError, OSError, ValueError, KeyError) as error:
        raise SystemExit('Stopped: ' + str(error))
    print('Exported %d rows and %d archive files (%d bytes) from a %s source to %s'
          % (result['rows'], result['files'], result['bytes'], result['source'], args.output))
    for name, value in result['outputs'].items():
        print('  %s sha256 %s' % (name, value))
    if args.source == 'live':
        print('Warning: this manifest describes the live archive, not an independent off-host backup.')


if __name__ == '__main__':
    main()
