#!/bin/bash

# Lint and format

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"
local="$(go list -m)"
if [[ -z "${CI:-}" ]]; then
    go run golang.org/x/tools/cmd/goimports@latest -d -w -local "$local" .
else
    output="$(go run golang.org/x/tools/cmd/goimports@latest -d -local "$local" .)"
    printf "%s" "$output"
    [ -z "$output" ] || exit 1
fi

cd "$base_dir/python"
uv sync --all-extras
if [[ -z "${CI:-}" ]]; then
    uv tool run ruff check --fix
    uv tool run ruff format
else
    uv tool run ruff check
    uv tool run ruff format --check
fi
.venv/bin/mypy . --exclude tests/runners --exclude tests/schemas
