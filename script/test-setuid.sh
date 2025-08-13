#!/bin/bash

# Test secure UID handling - verifies processes run with correct UID/GID
# Tests the hardened implementation where Go sets UID via syscall.Credential
# instead of Python calling setuid()

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"
port=${PORT:-5000}

cd "$base_dir"

./script/build.sh

name="test-setuid-$(date '+%s')"
read -r -d '' SCRIPT << EOF || :
set -e

# Procedure source URLs in production might be readable by root only
# So symlink won't work after dropping privilege
cp -r /src/python/tests/procedures /
chmod 700 /procedures/setuid
chmod 600 /procedures/setuid/*

find /src/dist -name '*.whl' -exec pip install {} \;
python3 -m cog.server.http --port $port --use-procedure-mode
EOF
docker run -it --rm --detach \
    --name "$name" \
    --entrypoint /bin/bash \
    --publish "$port:$port" \
    --volume "$PWD":/src python:latest \
    -c "$SCRIPT"

sleep 3  # wait for server startup
resp="$(mktemp)"
trap 'rm $resp; docker stop $name' EXIT
curl -fsSL -X POST \
    -H 'Content-Type: application/json' \
    --data '{"context":{"procedure_source_url": "file:///procedures/setuid", "replicate_api_token": "token"}, "input":{"p":"https://raw.githubusercontent.com/replicate/cog-runtime/refs/heads/main/.python-version"}}' \
    "http://localhost:$port/procedures" > "$resp"

status="$(jq --raw-output '.status' < "$resp")"
if [ "$status" != "succeeded" ]; then
    echo "Docker logs:"
    docker logs "$name"
    echo
    echo "Response:"
    cat "$resp"
    echo
    echo "FAILED"
    exit 1
else
    echo "Docker logs:"
    docker logs "$name"
    echo
    echo "PASSED"
fi
