#!/bin/bash
# This is a script to run the linters and validate formatting. In CI we use the
# github actions instead of this particular script. There might be some small
# delta in the results, but it's good enough for local development.

set -euo pipefail

go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.3.1 run ./...


output="$(go run github.com/daixiang0/gci@latest diff \
    --skip-generated \
    -s standard \
    -s default \
    -s "prefix(github.com/replicate/cog-runtime)" \
    .)"

if [[ -n "$output" ]]; then
    echo "gci formatting issues: run \`script/format.sh\` to fix"
    echo "$output"
    exit 1
fi

