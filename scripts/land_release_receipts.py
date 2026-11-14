#!/usr/bin/env python3
"""Write receipts for core images and viewer packages that Gitea Actions published.

Gitea Actions builds and publishes after a version bump reaches its copy of main,
but Gitea pull requests are off, so it cannot land the receipt. This runs on
GitHub after the same bump: it waits for the published artifact, checks it
against the source, and writes the receipt with the existing receipt scripts.
Registry reads are anonymous. The caller commits the written files.
"""
import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
REGISTRY_HOST = 'https://git.subcult.tv'
IMAGE = 'subculture-collective/archive-core'
NPM = 'https://git.subcult.tv/api/packages/subculture-collective/npm/'
MANIFEST_TYPES = ', '.join([
    'application/vnd.oci.image.index.v1+json', 'application/vnd.oci.image.manifest.v1+json',
    'application/vnd.docker.distribution.manifest.list.v2+json', 'application/vnd.docker.distribution.manifest.v2+json'])


def git(*args, cwd=ROOT):
    return subprocess.check_output(['git', *args], cwd=cwd, text=True).strip()


def fetch(url, headers=None, limit=8_000_000):
    request = urllib.request.Request(url, headers={'User-Agent': 'arkhiv-release-receipts', **(headers or {})})
    with urllib.request.urlopen(request, timeout=30) as response:
        data = response.read(limit + 1)
        if len(data) > limit:
            raise ValueError('Registry response exceeds the supported limit')
        return data, response.headers


class Registry:
    def __init__(self):
        self.token = json.loads(fetch(REGISTRY_HOST + '/v2/token?service=container_registry&scope=' +
                                      urllib.parse.quote('repository:' + IMAGE + ':pull'))[0])['token']

    def get(self, path, accept=MANIFEST_TYPES):
        return fetch(REGISTRY_HOST + '/v2/' + IMAGE + path, {'Authorization': 'Bearer ' + self.token, 'Accept': accept})

    def image(self, tag):
        """Return (digest, labels) for a tag, or None when the tag is not published."""
        try:
            body, headers = self.get('/manifests/' + tag)
        except urllib.error.HTTPError as error:
            if error.code == 404:
                return None
            raise
        digest = headers['Docker-Content-Digest']
        manifest = json.loads(body)
        if 'manifests' in manifest:
            entry = next(m for m in manifest['manifests'] if m.get('platform', {}).get('architecture') == 'amd64')
            manifest = json.loads(self.get('/manifests/' + entry['digest'])[0])
        config = json.loads(self.get('/blobs/' + manifest['config']['digest'], '*/*')[0])
        return digest, config.get('config', {}).get('Labels') or {}


def core_revision(candidates, lookup, version):
    """Pick the first candidate revision whose published image is this core version."""
    for revision in candidates:
        found = lookup(revision)
        if not found:
            continue
        digest, labels = found
        if labels.get('org.subcult.archive-artifact') != 'core' or labels.get('org.opencontainers.image.revision') != revision:
            raise ValueError('Published image identity does not match its tag: ' + revision)
        if labels.get('org.opencontainers.image.version') == version:
            return revision, digest
    return None


def candidates(start):
    """The start revision, then later main commits that contain it, oldest first."""
    git('fetch', '--quiet', 'origin', 'main')
    later = git('rev-list', '--reverse', '--ancestry-path', start + '..origin/main').split()
    return [start] + later


def write_core(version, revision, digest):
    receipt = ROOT / 'docs/releases' / ('core-release-' + version + '.json')
    with tempfile.TemporaryDirectory() as tree:
        git('worktree', 'add', '--quiet', '--detach', tree, revision)
        try:
            subprocess.run(['python3', 'scripts/core_release.py', '--engine-digest', 'git.subcult.tv/' + IMAGE + '@' + digest,
                            '--output', str(receipt)], cwd=tree, check=True)
        finally:
            git('worktree', 'remove', '--force', tree)
    return receipt


def viewer_tarball(version):
    """Return the published tarball bytes, or None while the version is not published."""
    metadata = json.loads(fetch(NPM + urllib.parse.quote('@subcult/archive-viewer', safe='@'))[0])
    entry = metadata.get('versions', {}).get(version)
    if not entry:
        return None
    url = entry['dist']['tarball']
    if not url.startswith(NPM):
        raise ValueError('Package tarball is outside its registry')
    return fetch(url, limit=4_000_000)[0]


def write_viewer(version, data):
    work = Path(tempfile.mkdtemp())
    try:
        package = work / ('subcult-archive-viewer-' + version + '.tgz')
        package.write_bytes(data)
        subprocess.run(['python3', 'scripts/viewer_receipt.py', '--package', str(package)], cwd=ROOT, check=True)
    finally:
        shutil.rmtree(work)
    return ROOT / 'docs/releases' / ('viewer-release-' + version + '.json')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--wait-minutes', type=int, default=150)
    parser.add_argument('--poll-seconds', type=int, default=60)
    args = parser.parse_args()
    head = git('rev-parse', 'HEAD')
    core = json.loads((ROOT / 'docs/reference/core-version.json').read_text())['version']
    viewer = json.loads((ROOT / 'packages/viewer/package.json').read_text())['version']
    for value in (core, viewer):
        if not re.fullmatch(r'\d+\.\d+\.\d+', value):
            raise SystemExit('Expected stable semantic versions')
    pending = {kind for kind, name in (('core', 'core-release-' + core), ('viewer', 'viewer-release-' + viewer))
               if not (ROOT / 'docs/releases' / (name + '.json')).exists()}
    deadline = time.monotonic() + args.wait_minutes * 60
    while pending:
        if 'core' in pending:
            found = core_revision(candidates(head), Registry().image, core)
            if found:
                print('Core', core, 'published from', found[0], 'as', found[1])
                write_core(core, *found)
                pending.discard('core')
        if 'viewer' in pending:
            data = viewer_tarball(viewer)
            if data is not None:
                print('Viewer', viewer, 'published;', write_viewer(viewer, data))
                pending.discard('viewer')
        if pending:
            if time.monotonic() > deadline:
                raise SystemExit('Not published in time: ' + ', '.join(sorted(pending)))
            time.sleep(args.poll_seconds)
    print('Receipts are current.')


if __name__ == '__main__':
    main()
