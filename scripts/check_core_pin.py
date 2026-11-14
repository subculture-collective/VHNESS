"""Check the reference deployment's independent headless core receipt and pin."""
import json
import re
from pathlib import Path

from core_release import validate_core


def main():
    root = Path(__file__).resolve().parents[1]
    receipt = json.loads((root / 'deploy/core-release.json').read_text())
    consumer = {'mode': 'api', 'adoption': 'core', 'rollout': 'reviewed-pr',
                'media': ['print', 'audio', 'video'], 'checks': [['make', 'verify-core']]}
    validate_core(receipt, consumer, receipt)
    pin = (root / 'deploy/archive-core.env').read_text()
    if pin != 'ARCHIVE_ENGINE_IMAGE=' + receipt['engine_digest'] + '\n':
        raise SystemExit('Core pin differs from the reviewed independent release')
    # The deployable core wrapper must add labels only, on top of the image CI
    # passes in from deploy/archive-core.env. CI also compares the built layers.
    dockerfile = re.sub(r'\\\n', ' ', (root / 'deploy/core.Dockerfile').read_text())
    steps = [line.split(None, 1) for line in dockerfile.splitlines()
             if line.strip() and not line.lstrip().startswith('#')]
    if not {s[0].upper() for s in steps} <= {'ARG', 'FROM', 'LABEL'}:
        raise SystemExit('core.Dockerfile may only use ARG, FROM and LABEL')
    if [s[1].strip() for s in steps if s[0].upper() == 'FROM'] != ['${CORE_IMAGE}']:
        raise SystemExit('core.Dockerfile must build FROM ${CORE_IMAGE}')
    if steps[0] != ['ARG', 'CORE_IMAGE']:
        raise SystemExit('CORE_IMAGE must be declared first, without a default')
    print('Independent headless core pin verified:', receipt['version'], receipt['source_revision'])


if __name__ == '__main__':
    main()
