#!/usr/bin/env python3
"""Apply prepared print migration inputs with the vhness operator commands.

Walks migration-plan.json in order: migrate, collection, retain-legacy,
import-source, import-catalog, review-transport and restore-print-index. A step
whose exact input the database already holds is skipped, so a rerun changes
nothing. The first failure stops the run. Every run writes a receipt.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
from urllib.parse import parse_qs, unquote, urlsplit

INDEX_FIELDS = ('snapshot_sha256', 'original_sha256', 'pages', 'text_status', 'tool', 'text_file',
                'text_sha256', 'cover_path', 'cover_sha256', 'cover_bytes')


class ApplyError(Exception):
    pass


def digest(data):
    return hashlib.sha256(data).hexdigest()


def sha(value):
    if not isinstance(value, str) or not re.fullmatch(r'[0-9a-f]{64}', value):
        raise ApplyError('Invalid content identity in plan')
    return value


def plan_steps(directory, notes, budget=None, reserve=None):
    """Return the ordered operator steps for a prepared output directory."""
    plan_file = directory / 'migration-plan.json'
    plan = json.loads(plan_file.read_text())
    collections = sorted(plan['collections'])
    if not collections:
        raise ApplyError('Plan has no collection')

    def read(name):
        path = directory / name
        if not path.is_file() or path.is_symlink():
            raise ApplyError('Plan input missing: ' + name)
        return path, path.read_bytes()

    steps = [{'command': 'migrate', 'args': []}]
    for collection in collections:
        steps.append({'command': 'collection', 'collection': collection, 'budget': budget, 'reserve': reserve,
                      'args': ['--collection', collection, '--policy', 'local_preserve',
                               '--budget', str(budget), '--reserve', str(reserve)]})
    for collection in collections:
        path, raw = read('retained-export-' + collection + '.json')
        steps.append({'command': 'retain-legacy', 'collection': collection, 'file': path, 'sha256': digest(raw),
                      'args': ['--collection', collection, '--file', str(path), '--notes', notes]})
    for source in plan['sources']:
        path, raw = read('source-' + sha(source['sha256']) + '.json')
        if digest(raw) != source['sha256']:
            raise ApplyError('Source input differs from the plan: ' + path.name)
        steps.append({'command': 'import-source', 'item': source['item_id'], 'file': path, 'sha256': source['sha256'],
                      'args': ['--id', source['item_id'], '--collection', collections[0], '--file', str(path)]})
    items = sorted(directory.glob('item-*.json'))
    representations = 0
    for path in items:
        raw = path.read_bytes()
        item = json.loads(raw)
        if path.name != 'item-' + sha(item['id']) + '.json':
            raise ApplyError('Item input name differs from its identity: ' + path.name)
        ids = sorted(rep['id'] for rep in item['representations'])
        representations += len(ids)
        steps.append({'command': 'import-catalog', 'item': item['id'], 'representations': ids, 'file': path,
                      'sha256': digest(raw), 'args': ['--file', str(path), '--notes', notes]})
    if len(items) != plan['documents'] or representations != plan['variants']:
        raise ApplyError('Item inputs differ from the plan document and variant counts')
    for kind, prefix, command in (('reviews', 'review-', 'review-transport'), ('indexes', 'index-', 'restore-print-index')):
        for entry in plan[kind]:
            path, raw = read(prefix + sha(entry['representation_id']) + '.json')
            data = json.loads(raw)
            args = ['--id', entry['item_id'], '--representation', entry['representation_id'],
                    '--collection', entry['collection'], '--file', str(path)]
            if command == 'review-transport' and data['review']['public']:
                args.append('--approve-public')
            steps.append({'command': command, 'item': entry['item_id'], 'representation': entry['representation_id'],
                          'collection': entry['collection'], 'file': path, 'sha256': digest(raw), 'input': data,
                          'args': args})
    return plan_file, steps


def expected_index(data):
    expected = {key: data[key] for key in INDEX_FIELDS}
    expected['sections'] = {str(page): text for page, text in data['sections'].items() if text.strip()}
    return expected


def current_state(state, step):
    """Compare the database with a step's input: 'missing', 'present' or a difference message."""
    command = step['command']
    if command == 'collection':
        found = state.collection(step['collection'])
        if found is None:
            return 'missing'
        if found['policy'] != 'local_preserve':
            return 'collection exists without local preservation'
        if step['budget'] is not None and (found['budget'], found['reserve']) != (step['budget'], step['reserve']):
            return 'collection budget or reserve differs; review it with review-budget'
        return 'present'
    if command == 'retain-legacy':
        return 'present' if state.retained_export(step['collection'], step['sha256']) else 'missing'
    if command == 'import-source':
        found = state.snapshot(step['sha256'])
        if found is None:
            return 'missing'
        return 'present' if found == step['item'] else 'source snapshot belongs to another item'
    if command == 'import-catalog':
        found = state.catalog_item(step['item'])
        if found is None:
            return 'missing'
        return 'present' if found == step['representations'] else 'catalog item exists with other representations'
    if command == 'review-transport':
        found = state.transport_review(step['item'], step['representation'], step['collection'])
        if found is None:
            return 'missing'
        return 'present' if found == step['input'] else 'current transport review differs; review it explicitly'
    if command == 'restore-print-index':
        found = state.print_index(step['item'], step['representation'])
        if found is None:
            return 'missing'
        return 'present' if found == expected_index(step['input']) else 'current print index differs'
    raise ApplyError('Unknown step ' + command)


def describe(step):
    for key in ('representation', 'item', 'collection'):
        if key in step:
            return step[key]
    return ''


def apply(directory, run, state, receipt_path, notes=None, budget=None, reserve=None, environ=os.environ, now=None, log=print):
    """Run every missing step in order. `run(argv)` returns an exit code; `state` reads the database."""
    now = now or (lambda: datetime.now(timezone.utc).isoformat(timespec='seconds'))
    plan_file = directory / 'migration-plan.json'
    plan_sha = digest(plan_file.read_bytes())
    receipt = {'plan': str(plan_file), 'plan_sha256': plan_sha, 'started_at': now(), 'status': 'failed', 'steps': []}
    try:
        _, steps = plan_steps(directory, notes or 'Applied print migration plan ' + plan_sha, budget, reserve)
        if any(s['command'] == 'restore-print-index' for s in steps):
            unset = [name for name in ('VHNESS_MEDIA', 'VHNESS_DERIVATIVES') if not environ.get(name)]
            if unset:
                raise ApplyError('Set ' + ' and '.join(unset) + ' to the recovered archive root before restoring print indexes')
        for number, step in enumerate(steps, 1):
            entry = {'step': number, 'command': step['command'], 'target': describe(step)}
            if 'file' in step:
                entry.update(input=step['file'].name, input_sha256=step['sha256'])
            receipt['steps'].append(entry)
            if step['command'] == 'migrate':
                found = 'missing'  # migrate applies only pending migrations; rerunning it is a no-op
            else:
                found = current_state(state, step)
            if found == 'missing' and step['command'] == 'collection' and step['budget'] is None:
                found = 'collection is not configured; pass --budget and --reserve to create it'
            if found == 'present':
                entry['outcome'] = 'unchanged'
                log('unchanged', step['command'], entry['target'])
                continue
            if found != 'missing':
                raise ApplyError('Step %d %s %s: %s' % (number, step['command'], entry['target'], found))
            code = run([step['command']] + step['args'])
            if code != 0:
                raise ApplyError('Step %d %s %s failed with exit code %d' % (number, step['command'], entry['target'], code))
            if step['command'] != 'migrate' and current_state(state, step) != 'present':
                raise ApplyError('Step %d %s %s succeeded but the database does not hold its input' % (number, step['command'], entry['target']))
            entry['outcome'] = 'ran' if step['command'] == 'migrate' else 'applied'
            log(entry['outcome'], step['command'], entry['target'])
        receipt['status'] = 'complete'
    except (ApplyError, OSError, ValueError, KeyError) as error:
        receipt['failure'] = str(error) if isinstance(error, ApplyError) else '%s: %s' % (type(error).__name__, error)
        if receipt['steps'] and 'outcome' not in receipt['steps'][-1]:
            receipt['steps'][-1]['outcome'] = 'failed'
    finally:
        receipt['finished_at'] = now()
        outcomes = [s.get('outcome') for s in receipt['steps']]
        receipt['counts'] = {name: outcomes.count(name) for name in ('applied', 'unchanged', 'ran', 'failed')}
        with open(receipt_path, 'x') as handle:
            handle.write(json.dumps(receipt, indent=2) + '\n')
        os.chmod(receipt_path, 0o600)
    return receipt


class PsqlState:
    """Read-only database checks through psql. Values are passed as psql variables."""

    def __init__(self, command, env):
        self.command, self.env = command, env

    def query(self, sql, **variables):
        argv = list(self.command) + ['-X', '-A', '-t', '-q', '-v', 'ON_ERROR_STOP=1']
        for name, value in variables.items():
            argv += ['-v', '%s=%s' % (name, value)]
        result = subprocess.run(argv, input=sql, capture_output=True, text=True, env=self.env)
        if result.returncode != 0:
            lines = result.stderr.strip().splitlines()
            raise ApplyError('Database check failed' + (': ' + lines[-1] if lines else ''))
        value = result.stdout.strip()
        return json.loads(value) if value else None

    def collection(self, collection):
        return self.query("SELECT json_build_object('policy',policy,'budget',budget,'reserve',reserve) FROM collections WHERE id=:'c';", c=collection)

    def retained_export(self, collection, sha256):
        return self.query("SELECT 'true' FROM retained_legacy_exports WHERE collection_id=:'c' AND module='print' AND sha256=:'s';", c=collection, s=sha256)

    def snapshot(self, sha256):
        return self.query("SELECT to_json(item_id) FROM snapshots WHERE sha256=:'s';", s=sha256)

    def catalog_item(self, item):
        return self.query("SELECT COALESCE(json_agg(r.id ORDER BY r.id) FILTER (WHERE r.id IS NOT NULL),'[]') FROM catalog_items i LEFT JOIN catalog_representations r ON r.item_id=i.id WHERE i.id=:'i' GROUP BY i.id;", i=item)

    def transport_review(self, item, representation, collection):
        return self.query("SELECT json_build_object('review',review,'preservation',preservation) FROM representation_transport_reviews WHERE item_id=:'i' AND representation_id=:'r' AND collection_id=:'c';", i=item, r=representation, c=collection)

    def print_index(self, item, representation):
        fields = ','.join("'%s',p.%s" % (f, f) for f in INDEX_FIELDS)
        return self.query("SELECT json_build_object(" + fields + ",'sections',(SELECT COALESCE(json_object_agg(t.page::text,t.text),'{}') FROM print_page_text t WHERE t.item_id=p.item_id AND t.representation_id=p.representation_id AND t.file=p.file)) FROM print_indexes p WHERE p.item_id=:'i' AND p.representation_id=:'r';", i=item, r=representation)


def libpq_env(environ):
    """Translate DATABASE_URL (or DATABASE_URL_FILE) into libpq variables, keeping it off the command line."""
    env = dict(environ)
    url = environ.get('DATABASE_URL', '')
    if environ.get('DATABASE_URL_FILE'):
        url = Path(environ['DATABASE_URL_FILE']).read_text().strip()
    if not url:
        return env
    parts = urlsplit(url)
    for name, value in (('PGHOST', parts.hostname), ('PGPORT', parts.port), ('PGUSER', parts.username),
                        ('PGPASSWORD', parts.password), ('PGDATABASE', parts.path.lstrip('/'))):
        if value:
            env[name] = unquote(str(value))
    for key, values in parse_qs(parts.query).items():
        if key == 'sslmode':
            env['PGSSLMODE'] = values[-1]
    return env


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', required=True, type=Path, help='Directory written by prepare_print_migration.py')
    parser.add_argument('--receipt', type=Path, help='New receipt file (default: apply-receipt-<UTC time>.json in the plan directory)')
    parser.add_argument('--vhness', default='vhness', help='Operator command prefix, for example "docker compose exec -T api vhness"')
    parser.add_argument('--psql', default='psql', help='Read-only psql command prefix for state checks')
    parser.add_argument('--notes', help='Review notes recorded by retain-legacy and import-catalog')
    parser.add_argument('--budget', type=int, help='Local byte budget when the collection does not exist yet')
    parser.add_argument('--reserve', type=int, help='Free-space reserve when the collection does not exist yet')
    args = parser.parse_args()
    if (args.budget is None) != (args.reserve is None):
        parser.error('--budget and --reserve go together')
    receipt = args.receipt or args.plan / ('apply-receipt-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '.json')
    env = libpq_env(os.environ)
    vhness = shlex.split(args.vhness)
    result = apply(args.plan, lambda argv: subprocess.run(vhness + argv).returncode,
                   PsqlState(shlex.split(args.psql), env), receipt, args.notes, args.budget, args.reserve)
    print('Receipt:', receipt)
    if result['status'] != 'complete':
        raise SystemExit('Stopped: ' + result['failure'])
    print('Applied', result['counts']['applied'], 'steps;', result['counts']['unchanged'], 'already present')


if __name__ == '__main__':
    main()
