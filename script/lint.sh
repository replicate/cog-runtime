#!/bin/bash
set -euo pipefail

# This is a helper script to run the linters and validate formatting in the same way CI does.

go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.3.1 run ./...

CI=1 ./script/check.sh go