#!/bin/bash

# Generate type stubs for development

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"
cd "$base_dir"

# Source shared functions
source "$base_dir/script/functions.sh"

echo "Generating type stubs..."
run_nox -s stubs
echo "✓ Type stubs generated successfully"