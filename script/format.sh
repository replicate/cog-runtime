#!/bin/bash
set -euo pipefail

go run github.com/daixiang0/gci@latest write  --skip-generated -s standard -s default -s "prefix(github.com/replicate/cog-runtime)" .
