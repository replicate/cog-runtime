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

export LOG_FORMAT=development
export PATH="$base_dir/python/.venv/bin:$PATH"
export PYTHONPATH="$base_dir/python"
rm -rf tmp
mkdir -p tmp
go run cmd/server/main.go \
    --working-dir tmp \
    --module-name "tests.runners.$module" \
    --class-name Predictor \
    "$@"
