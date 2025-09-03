#!/bin/bash

# Unit tests

set -euo pipefail

: "${GITHUB_ACTIONS:=}"

base_dir="$(git rev-parse --show-toplevel)"

cd "$base_dir"

# Source shared functions
source "$base_dir/script/functions.sh"

test_go() {
    format="dots-v2"
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
        format="github-actions"
    fi
    go run gotest.tools/gotestsum@latest --format "$format" ./... -- -timeout=30s "$@"
}


test_python() {
    # Use nox with current system Python and isolated venv
    run_nox -s test_current -- "$@"
}

test_python_all() {
    # Test all Python versions with nox
    run_nox -s tests -- "$@"
}

test_python_nox() {
    # Test single Python version with nox (faster for development)
    run_nox -s test -- "$@"
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
        python-all)
            test_python_all "$@"
            ;;
        python-nox)
            test_python_nox "$@"
            ;;
        *)
            echo "Unknown test $t. Available: go, python, python-all, python-nox"
            exit 1
            ;;
    esac
fi
