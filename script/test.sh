#!/bin/bash

# Unit tests

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"

test_go() {
    go test ./...
}

test_python() {
    cd "$base_dir/python"
    uv sync --all-extras
    .venv/bin/pytest
}

if [ $# -eq 0 ]; then
    test_go
    test_python
else
    for c in "$@"; do
        case "$c" in
            go)
                test_go
                ;;
            python)
                test_python
                ;;
            *)
                echo "Unknown test $c"
                exit 1
                ;;
        esac
    done
fi
