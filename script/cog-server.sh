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

# PYTHON=1 to run with legacy Cog
if [ -z "${PYTHON:-}" ]; then
    export LOG_FORMAT=development
    export PATH="$base_dir/python/.venv/bin:$PATH"
    export PYTHONPATH="$base_dir/python/coglet/_compat:$base_dir/python"
    rm -rf tmp
    mkdir -p tmp
    go run cmd/cog-server/main.go \
        --working-dir tmp \
        --module-name "tests.runners.$module" \
        --class-name Predictor \
        "$@"
else
    python_version="$(cat "$base_dir/python/.python-version")"
    cd "$base_dir/python/tests/runners"
    cp "$module.py" predict.py
    trap "rm -f predict.py" EXIT
    uv run --python "$python_version" --with cog python3 -m cog.server.http "$@"
fi
