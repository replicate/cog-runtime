#!/bin/bash

# Unit tests

set -euo pipefail

: "${GITHUB_ACTIONS:=}"

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"

test_go() {
    format="dots-v2"
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
        format="github-actions"
    fi
    go run gotest.tools/gotestsum@latest --format "$format" ./... -- -timeout=30s "$@"
}

test_python() {
    # Only add -n auto if -n isn't already specified (supports -n <digits> or -n auto)
    if [[ ! "$*" =~ -n[[:space:]]*([[:digit:]]+|auto) ]]; then
        .venv/bin/pytest "$@" -vv -n auto
    else
        .venv/bin/pytest "$@" -vv
    fi
}

if [ $# -eq 0 ]; then
    test_go
    test_python
else
    t=$1
    shift
    case "$t" in
        go)
            test_go "$@"
            ;;
        python)
            test_python "$@"
            ;;
        *)
            echo "Unknown test $t"
            exit 1
            ;;
    esac
fi
