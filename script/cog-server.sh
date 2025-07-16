#!/bin/bash

# Run Cog HTTP server

set -euo pipefail

if [ $# -lt 1 ]; then
    echo "Usage: $(basename "$0") <MODULE> [ARG]..."
    exit 1
fi

predict="$1"
shift

base_dir="$(git rev-parse --show-toplevel)"

MODULE=${MODULE:-runners}
CLASS=${CLASS:-Predictor}
MAX_CONCURRENCY=${MAX_CONCURRENCY:-0}

# go run looks for go.mod so we need to run inside base_dir
temp_dir="$base_dir/build/cog-$MODULE-$predict-$(date '+%s')"
mkdir -p "$temp_dir"
trap 'rm -rf $temp_dir' EXIT
cd "$temp_dir"
cp "$base_dir/python/tests/$MODULE/$predict.py" predict.py
echo "predict: \"predict.py:$CLASS\"" > cog.yaml

if [ $MAX_CONCURRENCY -gt 0 ]; then
    echo 'concurrency:' > cog.yaml
    echo "  max: $MAX_CONCURRENCY" > cog.yaml
fi

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
