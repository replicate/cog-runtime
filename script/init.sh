#!/bin/bash

# Initialize development environment

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"
uv sync --all-extras

# venv with legacy Cog
# python >= 3.11 for async tests
uv venv --python 3.13 .venv-legacy
export VIRTUAL_ENV="$base_dir/.venv-legacy"
export UV_PROJECT_ENVIRONMENT="$VIRTUAL_ENV"
uv pip install cog==0.16.5
