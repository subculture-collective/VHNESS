#!/usr/bin/env python3
"""Create one reviewed upgrade PR per release in an isolated registered consumer clone."""
import argparse
import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request

from upgrade_consumer import prepare, validate, select_consumer
from viewer_release import retained_core


def github_repository(consumer):
    """GitHub is the source for every consumer; a few registry names are older Gitea paths."""
    return consumer.get('github_repository', consumer['repository'])


def api(path, method='GET', payload=None):
    token = os.environ['ARCHIVE_UPGRADE_TOKEN']
    request = urllib.request.Request('https://api.github.com' + path,
        method=method, data=json.dumps(payload).encode() if payload is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json',
                 'X-GitHub-Api-Version': '2022-11-28', 'Content-Type': 'application/json', 'User-Agent': 'arkhiv-consumer-upgrades'})
    with urllib.request.urlopen(request, timeout=30) as response:
        raw = response.read()
        return json.loads(raw) if raw else None


def existing_proposal(base, target, branch, consumer, release, request):
    owner = base.split('/')[2]
    pulls = request(base + '/pulls?state=all&per_page=100&head=' + urllib.parse.quote(owner + ':' + branch, safe=':'))
    for pull in pulls:
        if pull['head']['ref'] == branch:
            if pull['base']['ref'] != target:
                raise ValueError('Existing proposal targets a different base')
            if pull['state'] == 'closed' and not (pull.get('merged') or pull.get('merged_at')):
                raise ValueError('Upgrade was declined; do not reopen automatically')
            content = request(base + '/contents/' + consumer['release_file'] + '?ref=' + urllib.parse.quote(branch, safe=''))
            proposed = json.loads(base64.b64decode(content['content']))
            if proposed != release:
                raise ValueError('Existing proposal differs from this release')
            return pull['html_url']
    return None


def schedule_merge(request, base, url):
    """Consumer repositories merge a ready pull request themselves once their required
    checks pass (their auto-merge workflow or the owner's merge timer), so nothing is
    requested here; a failed check leaves the pull request open for review."""
    print(url, 'merges once its required checks pass', flush=True)
    return True


def proposal(release, consumer, clone, artifact, request=api, base_ref=None, auto_merge=False):
    repository = github_repository(consumer)
    base = '/repos/' + repository
    metadata = request(base)
    if not metadata.get('permissions', {}).get('push'):
        raise ValueError('The upgrade token cannot push to ' + repository)
    target = base_ref or metadata['default_branch']
    api_only = consumer.get('mode') == 'api'
    viewer_only = consumer.get('mode') == 'viewer'
    branch = ('archive-core-upgrade/v' if api_only else 'archive-viewer-upgrade/v' if viewer_only else 'archive-upgrade/v') + release['version'] + '-' + release['source_revision']
    # Validate the base consumer before consulting or creating a proposal.
    current = json.loads((clone / consumer['release_file']).read_text())
    core = retained_core(clone, consumer)[0] if viewer_only else None
    validate(release, consumer, current, artifact, core)
    if current == release:
        return None
    existing = existing_proposal(base, target, branch, consumer, release, request)
    if existing:
        if auto_merge:
            schedule_merge(request, base, existing)
        return existing
    subprocess.run(['git', 'switch', '-c', branch], cwd=clone, check=True)
    prepare(clone, consumer, release, artifact)
    files = [consumer[key] for key in (('release_file', 'engine_file') if api_only else ('package_file', 'lock_file', 'release_file'))]
    if consumer.get('mode', 'platform') == 'platform':
        files.append(consumer['engine_file'])
    subprocess.run(['git', 'add', '--', *files], cwd=clone, check=True)
    title = 'Upgrade shared archive ' + ('viewer' if viewer_only else 'platform') + ' to ' + release['version']
    subprocess.run(['git', '-c', 'user.name=SUBCULT Agent', '-c', 'user.email=info@subcult.tv', '-c', 'commit.gpgsign=false',
                    'commit', '-m', title], cwd=clone, check=True)
    subprocess.run(['git', 'push', 'origin', branch], cwd=clone, check=True)
    try:
        result = request(base + '/pulls', 'POST', {
            'base': target, 'head': branch,
            'title': title,
            'body': ('Updates only the immutable headless core pin and compatibility receipt. The consumer owns its entire frontend; no frontend package is required.\n\n'
                     if api_only else
                     'Updates only the independent viewer package, lockfile and SDK receipt. The reviewed core receipt, immutable image pin and consumer-owned interface remain unchanged.\n\n'
                     if viewer_only else
                     'Updates the frontend package, lockfile and release provenance receipt; the consumer-owned backend remains unchanged.\n\n'
                     if consumer.get('mode', 'platform') == 'components' else
                     'Updates the frontend lock, immutable engine digest and compatibility receipt together.\n\n') +
                    'Consumer checks passed in the isolated proposal checkout. Hosted checks and private qualification remain required. '
                    'This PR does not deploy the consumer.\n\nSource: `' + release['source_revision'] + '`; ' +
                    ('core digest: `' + release['engine_digest'] + '`.' if api_only else 'package SHA-256: `' + release['package_sha256'] + '`.'),
        })
    except (TimeoutError, ConnectionError, urllib.error.URLError) as error:
        if isinstance(error, urllib.error.HTTPError):
            raise
        # A timed-out POST may have created the PR. Read back its exact receipt
        # before considering it delivered; do not repeat the write blindly.
        existing = existing_proposal(base, target, branch, consumer, release, request)
        if existing:
            if auto_merge:
                schedule_merge(request, base, existing)
            return existing
        raise
    if auto_merge:
        schedule_merge(request, base, result['html_url'])
    return result['html_url']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--registry', type=Path, required=True)
    parser.add_argument('--consumer', required=True)
    parser.add_argument('--release', type=Path, required=True)
    parser.add_argument('--package-artifact', type=Path)
    parser.add_argument('--base-ref', help='Explicit reviewed adoption branch for a stacked proposal; defaults to the repository default branch')
    parser.add_argument('--auto-merge', action='store_true', help='Merge the proposal automatically once the consumer\'s required checks pass')
    args = parser.parse_args()
    registry = json.loads(args.registry.read_text())
    release = json.loads(args.release.read_text())
    consumer = select_consumer(registry, args.consumer, release)
    if consumer['adoption'] != ('core' if consumer.get('mode') == 'api' else 'package'):
        raise SystemExit('Consumer package adoption is pending')
    if args.base_ref:
        subprocess.run(['git', 'check-ref-format', '--branch', args.base_ref], check=True, stdout=subprocess.DEVNULL)
    with tempfile.TemporaryDirectory(prefix='archive-upgrade-') as work:
        clone = Path(work) / 'consumer'
        subprocess.run(['git', 'clone', '--quiet', 'https://github.com/' + github_repository(consumer) + '.git', str(clone)], check=True)
        if args.base_ref:
            subprocess.run(['git', 'switch', '--detach', 'origin/' + args.base_ref], cwd=clone, check=True)
        print(proposal(release, consumer, clone, args.package_artifact.resolve() if args.package_artifact else None, base_ref=args.base_ref, auto_merge=args.auto_merge) or 'Release already applied; no PR')


if __name__ == '__main__':
    main()
