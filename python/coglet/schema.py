import json
import os.path
import sys

from coglet import inspector, schemas


def main():
    if len(sys.argv) != 3:
        print(f'Usage {os.path.basename(sys.argv[0])} <MODULE> <CLASS>')
        sys.exit(1)

    p = inspector.create_predictor(sys.argv[1], sys.argv[2])
    s = schemas.to_json_schema(p)
    print(json.dumps(s, indent=2))


if __name__ == '__main__':
    main()
