#!/bin/bash

# Lint and format

set -uo pipefail

base_dir="$(git rev-parse --show-toplevel)"

# Source shared functions
source "$base_dir/script/functions.sh"

check_go() {
    echo "Checking Go..."
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
    echo "Checking Python..."
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


if [ $# -eq 0 ]; then
    check_go
    check_python
    # Skip check_stubs - typecheck already generates stubs locally
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
                echo "Unknown check $c. Available: go, python"
                exit 1
                ;;
        esac
    done
fi