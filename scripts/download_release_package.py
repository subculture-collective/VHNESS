"""Download the registered package and verify the reviewed release checksum."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import urllib.parse
import urllib.request

REGISTRY = 'https://git.subcult.tv/api/packages/subculture-collective/npm/'


class RegistryRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, url):
        if not url.startswith(REGISTRY):
            raise ValueError('Package download redirected outside its registry')
        return super().redirect_request(request, response, code, message, headers, url)


def download(release, output):
    if release['registry'] != REGISTRY or release['package'] != '@subcult/archive-viewer':
        raise ValueError('Unregistered package registry')
    token = os.environ.get('ARCHIVE_PACKAGE_READ_TOKEN', '')
    def get(url, maximum):
        if not url.startswith(REGISTRY):
            raise ValueError('Package download changed registry ownership')
        request = urllib.request.Request(url, headers={'User-Agent': 'tea', **({'Authorization': 'token ' + token} if token else {})})
        with urllib.request.build_opener(RegistryRedirect()).open(request, timeout=30) as response:
            if not response.url.startswith(REGISTRY):
                raise ValueError('Package download redirected outside its registry')
            data = response.read(maximum + 1)
        if len(data) > maximum:
            raise ValueError('Package response exceeds the supported limit')
        return data
    # Gitea answers 404 when the scope's '@' is percent-encoded; only the '/' is escaped.
    metadata = json.loads(get(REGISTRY + urllib.parse.quote(release['package'], safe='@'), 2_000_000))
    version = metadata['versions'][release['version']]
    if version['name'] != release['package'] or version['version'] != release['version']:
        raise ValueError('Registry package identity mismatch')
    data = get(version['dist']['tarball'], 4_000_000)
    if hashlib.sha256(data).hexdigest() != release['package_sha256']:
        raise ValueError('Registry package checksum mismatch')
    if output.exists():
        if output.read_bytes() != data:
            raise ValueError('Retain the existing artifact; do not overwrite')
    else:
        output.write_bytes(data)
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    download(json.loads(args.release.read_text()), args.output)
    print('Registry package checksum verified')
