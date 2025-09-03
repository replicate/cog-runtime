#!/bin/bash

# Lint and format

set -uo pipefail

base_dir="$(git rev-parse --show-toplevel)"

# Source shared functions
source "$base_dir/script/functions.sh"

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
    cd "$base_dir"
    if [[ -z "${CI:-}" ]]; then
        # Local dev: fix and format
        run_nox -s lint -- --fix
        run_nox -s format
        run_nox -s typecheck
    else
        # CI: use check_all session (lint + format --check + typecheck)
        run_nox -s check_all
    fi
}

check_stubs() {
    echo "Regenerating stub files..."
    cd "$base_dir"
    # Remove existing stubs to avoid duplication
    find python -name "*.pyi" -type f -delete
    PYTHONPATH=python npx -y pyright --createstub coglet || echo "Warning: coglet stub creation may have failed"
    PYTHONPATH=python npx -y pyright --createstub cog || echo "Warning: cog stub creation may have failed"
    # Move stubs from typings/ to alongside source
    if [[ -d "typings" ]]; then
        cp -rv typings/* python/
    fi
    # Cleanup
    rm -rf typings/
}

if [ $# -eq 0 ]; then
    check_go
    check_python
    check_stubs
else
    for c in "$@"; do
        case "$c" in
            go)
                check_go
                ;;
            python)
                check_python
                ;;
            stubs)
                check_stubs
                ;;
            *)
                echo "Unknown check $c"
                exit 1
                ;;
        esac
    done
fi