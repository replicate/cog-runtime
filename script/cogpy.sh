#!/bin/bash

# Run Cog HTTP server

set -euo pipefail

if [ $# -lt 1 ]; then
    echo "Usage: $(basename "$0") <MODULE> [ARG]..."
    exit 1
fi

module="$1"
shift

base_dir="$(git rev-parse --show-toplevel)"
python_version="$(cat "$base_dir/python/.python-version")"

cd "$base_dir/python/tests/runners"
cp "$module.py" predict.py
trap "rm -f predict.py" EXIT
uv run --python "$python_version" --with cog python3 -m cog.server.http "$@"
