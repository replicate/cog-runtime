#!/bin/bash

# Build binaries

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"

rm -rf build
rm -rf dist
rm -rf python/cog/cog-*

# Create "clet", coglet lite for web downloading
# This does not contain the go binaries
.venv/bin/python3 -m build -w
for file in dist/coglet-*; do
  [ -e "$file" ] || continue
  filename=$(basename "$file")
  new_filename=${filename/coglet-/clet-}
  mv "$file" "dist/$new_filename"
  echo "Renamed: $file -> dist/$new_filename"
done

# Export Python version to Go
uv run --with setuptools_scm python3 -m setuptools_scm > internal/util/version.txt

# Binaries are bundled in Python wheel
for os in darwin linux; do
    for arch in amd64 arm64; do
        CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -o python/cog/cog-$os-$arch ./cmd/cog
    done
done

.venv/bin/python3 -m build -w
