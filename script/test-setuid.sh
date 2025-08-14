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
trap 'rm $resp1 $resp2; docker stop $name' EXIT
resp1="$(mktemp)"
resp2="$(mktemp)"
curl -fsSL -X POST \
    -H 'Content-Type: application/json' \
    --data '{"context":{"procedure_source_url": "file:///procedures/setuid", "replicate_api_token": "token"}, "input":{"p":"https://raw.githubusercontent.com/replicate/cog-runtime/refs/heads/main/.python-version","i":0}}' \
    "http://localhost:$port/procedures" > "$resp1" &
curl -fsSL -X POST \
    -H 'Content-Type: application/json' \
    --data '{"context":{"procedure_source_url": "file:///procedures/setuid", "replicate_api_token": "token"}, "input":{"p":"https://raw.githubusercontent.com/replicate/cog-runtime/refs/heads/main/.python-version","i":1}}' \
    "http://localhost:$port/procedures" > "$resp2" &

sleep 2  # wait for predictions to finish

status1="$(jq --raw-output '.status' < "$resp1")"
status2="$(jq --raw-output '.status' < "$resp2")"
if [ "$status1" != "succeeded" ] || [ "$status2" != "succeeded" ]; then
    echo "Docker logs:"
    docker logs "$name"
    echo
    echo "Response 1:"
    cat "$resp1"
    echo
    echo "Response 2:"
    cat "$resp2"
    echo
    echo "FAILED"
    exit 1
else
    echo "Docker logs:"
    docker logs "$name"
    echo
    echo "PASSED"
fi
