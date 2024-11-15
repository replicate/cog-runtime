#!/bin/bash

# Unit tests

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"
go test ./...

cd "$base_dir/python"
uv sync --all-extras
.venv/bin/pytest
