#!/bin/bash

# Run Cog HTTP server

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"

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
    go run "$base_dir/cmd/cog/main.go" server "${args[@]}" --use-procedure-mode "$@"
else
    cd "$base_dir/../pipelines-runtime"
    export PROCEDURE_CACHE_PATH=/tmp/procedures
    "$base_dir/.venv-procedure/bin/python3" -m cog.server.http "$@"
fi
