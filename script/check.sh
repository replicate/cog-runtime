#!/bin/bash

# Lint and format

set -uo pipefail

base_dir="$(git rev-parse --show-toplevel)"

check_go() {
    cd "$base_dir"
    local="$(go list -m)"
    if [[ -z "${CI:-}" ]]; then
        go run golang.org/x/tools/cmd/goimports@latest -d -w -local "$local" .
        go run mvdan.cc/gofumpt@latest -extra -l -w .
    else
        goimports="$(go run golang.org/x/tools/cmd/goimports@latest -d -local "$local" .)"
        printf "%s" "$goimports"
        gofumpt="$(go run mvdan.cc/gofumpt@latest -extra -d .)"
        printf "%s" "$gofumpt"
        [ -z "$goimports" ] || exit 1
        [ -z "$gofumpt" ] || exit 1
    fi
}

check_python() {
    set -e
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