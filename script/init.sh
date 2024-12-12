#!/bin/bash

# Initialize development environment

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"
python_version="$(cat "$base_dir/.python-version")"

cd "$base_dir"
uv sync --all-extras

# venv with legacy Cog
uv venv --python "$python_version" .venv-legacy
export VIRTUAL_ENV="$base_dir/.venv-legacy"
uv pip install cog
