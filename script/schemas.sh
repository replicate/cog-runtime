#!/bin/bash

# Generate Open API JSON schemas from predictors

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"
schemas_dir="$base_dir/python/tests/schemas"

cd "$schemas_dir"

trap "rm -f predict.py" EXIT
for f in *.py; do
    echo "Generating schema for $(basename "$f")"
    ln -fs "$f" predict.py
    "$base_dir/python/.venv-legacy/bin/python3" -m cog.command.openapi_schema > "$(basename "$f" .py).json"
done
