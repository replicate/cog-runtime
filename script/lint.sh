#!/bin/bash
set -euo pipefail

: "${GITHUB_ACTIONS:=}"

GCI_COMMAND="write"
GIT_ROOT=$(git rev-parse --show-toplevel)

if [ "$GITHUB_ACTIONS" != "true" ]; then
    GCI_COMMAND="diff"
fi

go run github.com/daixiang0/gci@latest ${COMMAND:-"diff"}  --skip-generated -s standard -s default -s "prefix(github.com/replicate/cog-runtime)" $GIT_ROOT
