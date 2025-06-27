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

MODULE=${MODULE:-runners}
cd "$base_dir/python/tests/$MODULE"
ln -fs "$module.py" predict.py
trap "rm -f predict.py" EXIT
# PYTHON=1 to run with legacy Cog
if [ -z "${PYTHON:-}" ]; then
    export LOG_FORMAT=development
    export PATH="$base_dir/.venv/bin:$PATH"
    PYTHON_VERSION="$(cat "$base_dir/.python-version")"
    export PYTHONPATH="$base_dir/python:$base_dir/.venv/lib/python$PYTHON_VERSION/site-packages"
    args=()
    if [ -n "${PORT:-}" ]; then
        args+=(--port "$PORT")
    fi
    go run "$base_dir/cmd/cog/main.go" server "${args[@]}" "$@"
else
    "$base_dir/.venv-legacy/bin/python3" -m cog.server.http "$@"
fi
