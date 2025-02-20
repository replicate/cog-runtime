import json
import os.path
import sys

from coglet import inspector, schemas


def main():
    if len(sys.argv) != 3:
        print(f'Usage {os.path.basename(sys.argv[0])} <MODULE> <CLASS>')
        sys.exit(1)

    # Some libraries print progress upon import and mess up schema JSON
    _stdout_write = sys.stdout.write
    _stderr_write = sys.stderr.write
    sys.stdout.write = lambda out: len(out)
    sys.stderr.write = lambda out: len(out)
    p = inspector.create_predictor(sys.argv[1], sys.argv[2])
    s = schemas.to_json_schema(p)
    sys.stdout.write = _stdout_write
    sys.stderr.write = _stderr_write

    print(json.dumps(s, indent=2))


if __name__ == '__main__':
    main()
