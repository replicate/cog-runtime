#!/bin/bash

# Unit tests

set -euo pipefail

: "${GITHUB_ACTIONS:=}"

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"

test_go() {
    go run gotest.tools/gotestsum@latest --format testname ./... -- -timeout=30s
}

test_python() {
    .venv/bin/pytest "$@" -vv
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
