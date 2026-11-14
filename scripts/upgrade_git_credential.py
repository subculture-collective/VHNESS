"""Provide only the scoped upgrade identity to registered consumer Git remotes."""
import json
import os
from pathlib import Path
import sys

if len(sys.argv) == 2 and sys.argv[1] == 'get':
    fields = dict(line.rstrip('\n').split('=', 1) for line in sys.stdin if '=' in line)
    registry = json.loads((Path(__file__).resolve().parents[1] / 'docs/reference/consumer-registry.json').read_text())
    paths = {c.get('github_repository', c['repository']) + '.git' for c in registry['consumers']}
    if fields.get('protocol') == 'https' and fields.get('host') == 'github.com' and fields.get('path') in paths:
        print('username=x-access-token')
        print('password=' + os.environ['ARCHIVE_UPGRADE_TOKEN'])
