#!/bin/bash

# Generate Open API JSON schemas from predictors

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"
schemas_dir="$base_dir/python/tests/schemas"
python_version="$(cat "$base_dir/python/.python-version")"

cd "$schemas_dir"

trap "rm -f predict.py" EXIT
for f in *.py; do
    echo "Generating schema for $(basename "$f")"
    cp "$f" predict.py
    uv run --python "$python_version" --with cog python3 -m cog.command.openapi_schema > "$(basename "$f" .py).json"
done
