"""Propose a reviewed release for registered consumers with independent checks."""
import argparse
import json
import os
from pathlib import Path
import re
import shlex
import socket
import subprocess
import tempfile

from download_release_package import download
from propose_consumer_upgrade import github_repository, proposal



def consumer_test_database(inspected, run, repository, resolve=socket.gethostbyname):
    if inspected['Config']['Labels'].get('arkhiv.upgrade-database') != run:
        raise ValueError('Database belongs to another job')
    environment = dict(value.split('=', 1) for value in inspected['Config'].get('Env') or [])
    if environment.get('POSTGRES_USER') != 'dev' or environment.get('POSTGRES_PASSWORD') != 'disposable-dev-only':
        raise ValueError('Expected the disposable launcher database identity')
    addresses = [network['IPAddress'] for network in inspected['NetworkSettings']['Networks'].values()]
    if len(addresses) != 1 or not re.fullmatch(r'(\d{1,3}\.){3}\d{1,3}', addresses[0]):
        raise ValueError('Expected one job-owned database address')
    if resolve('db') != addresses[0]:
        raise ValueError('Launcher database alias resolves outside this job')
    database = 'upgrade_' + repository.lower().replace('/', '__').replace('-', '_')
    if not re.fullmatch(r'[a-z][a-z0-9_]{0,62}', database):
        raise ValueError('Invalid consumer test database name')
    return database, 'postgres://dev:disposable-dev-only@db:5432/' + database + '?sslmode=disable'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--kind', choices=['platform', 'core', 'viewer'], default='viewer')
    parser.add_argument('--adoption-refs', action='store_true', help='Explicitly stack on registered adoption branches that are still in review')
    parser.add_argument('--auto-merge', action='store_true', help='Merge each proposal automatically once the consumer\'s required checks pass')
    args = parser.parse_args()
    if not re.fullmatch(r'\d+\.\d+\.\d+', args.version):
        raise SystemExit('Expected a stable semantic release version')
    root = Path(__file__).resolve().parents[1]
    prefix = {'core': 'core-release-', 'viewer': 'viewer-release-', 'platform': 'release-'}[args.kind]
    release = json.loads((root / 'docs/releases' / (prefix + args.version + '.json')).read_text())
    if release['version'] != args.version:
        raise SystemExit('Release filename and metadata disagree')
    if args.kind != 'core' and release['package_file'] != 'subcult-archive-viewer-' + args.version + '.tgz':
        raise SystemExit('Unexpected release package filename')
    registry = json.loads((root / 'docs/reference/consumer-registry.json').read_text())
    # Reset inherited helpers; never use a human or checkout action credential.
    os.environ.update({'GIT_CONFIG_COUNT': '3', 'GIT_CONFIG_KEY_0': 'credential.helper', 'GIT_CONFIG_VALUE_0': '',
        'GIT_CONFIG_KEY_1': 'credential.helper',
        'GIT_CONFIG_VALUE_1': '!' + shlex.join(['python3', str(root / 'scripts/upgrade_git_credential.py')]),
        'GIT_CONFIG_KEY_2': 'credential.useHttpPath', 'GIT_CONFIG_VALUE_2': 'true', 'GIT_TERMINAL_PROMPT': '0'})
    with tempfile.TemporaryDirectory(prefix='archive-proposals-') as temporary:
        work = Path(temporary)
        artifact = None if args.kind == 'core' else download(release, work / release['package_file'])
        for consumer in registry['consumers']:
            lane = 'core' if consumer.get('mode') == 'api' else 'viewer' if consumer.get('mode') == 'viewer' else 'platform'
            if lane != args.kind:
                continue
            if consumer['adoption'] != ('core' if args.kind == 'core' else 'package'):
                print(consumer['repository'], 'package adoption pending', flush=True)
                continue
            base_ref = None
            if consumer.get('adoption_status') == 'pull-request':
                if not args.adoption_refs:
                    print(consumer['repository'], 'adoption remains in review; select --adoption-refs for a stacked proposal', flush=True)
                    continue
                base_ref = consumer['adoption_ref']
            clone = work / consumer['repository'].replace('/', '__')
            subprocess.run(['git', 'clone', '--quiet', 'https://github.com/' + github_repository(consumer) + '.git', str(clone)], check=True)
            if base_ref:
                subprocess.run(['git', 'switch', '--detach', 'origin/' + base_ref], cwd=clone, check=True)
                revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=clone, text=True).strip()
                if revision != consumer['adoption_revision']:
                    raise ValueError('Reviewed adoption branch moved; refresh its qualification before proposing')
            database_container = os.environ.get('ARCHIVE_UPGRADE_DATABASE_CONTAINER')
            if database_container:
                if not re.fullmatch(r'[0-9a-f]{64}', database_container):
                    raise ValueError('Expected one full job-owned database container identity')
                inspected = json.loads(subprocess.check_output(['docker', 'inspect', database_container]))[0]
                database, test_url = consumer_test_database(inspected, os.environ['ARCHIVE_UPGRADE_DATABASE_RUN'], consumer['repository'])
                subprocess.run(['docker', 'exec', database_container, 'createdb', '-U', 'dev', database], check=True)
                os.environ['TEST_DATABASE_URL'] = test_url
            result = proposal(release, consumer, clone, artifact, base_ref=base_ref, auto_merge=args.auto_merge)
            print(consumer['repository'], result or 'Release already applied; no PR', flush=True)


if __name__ == '__main__':
    main()
