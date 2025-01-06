#!/bin/bash

# Build binaries

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"
rm -rf dist
.venv/bin/python3 -m build -w

# Export Python version to Go
.venv/bin/python3 -c 'import coglet; print(coglet.__version__)' > internal/util/version.txt

for os in darwin linux; do
    for arch in amd64 arm64; do
        CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -o dist/cog-server-$os-$arch ./cmd/cog-server
    done
done
