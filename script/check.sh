#!/bin/bash

# Lint and format

set -euo pipefail

GCI_VERSION="v0.13.7"
base_dir="$(git rev-parse --show-toplevel)"

check_go() {
    cd "$base_dir"
    local="$(go list -m)"
    if [[ -z "${CI:-}" ]]; then
        go run github.com/daixiang0/gci@$GCI_VERSION write --skip-generated -s standard -s default -s "prefix(github.com/replicate/cog-runtime)" .
    else
        output="$(go run github.com/daixiang0/gci@$GCI_VERSION diff --skip-generated -s standard -s default -s "prefix(github.com/replicate/cog-runtime)" .)"
        printf "%s" "$output"
        [ -z "$output" ] || exit 1
    fi
}

check_python() {
    uv sync --all-extras
    if [[ -z "${CI:-}" ]]; then
        uv tool run ruff check --fix
        uv tool run ruff format
    else
        uv tool run ruff check
        uv tool run ruff format --check
    fi
    .venv/bin/mypy . --exclude build \
        --exclude python/tests/cases \
        --exclude python/tests/bad_inputs \
        --exclude python/tests/bad_predictors \
        --exclude python/tests/runners \
        --exclude python/tests/schemas
}

if [ $# -eq 0 ]; then
    check_go
    check_python
else
    for c in "$@"; do
        case "$c" in
            go)
                check_go
                ;;
            python)
                check_python
                ;;
            *)
                echo "Unknown check $c"
                exit 1
                ;;
        esac
    done
fi