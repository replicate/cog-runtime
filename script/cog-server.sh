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
    export PYTHONPATH="$base_dir/python"
    go run cmd/cog-server/main.go \
        --module-name "tests.runners.$module" \
        --class-name Predictor \
        "$@"
else
    cd "$base_dir/python/tests/runners"
    ln -fs "$module.py" predict.py
    trap "rm -f predict.py" EXIT
    "$base_dir/python/.venv-legacy/bin/python3" -m cog.server.http "$@"
fi
