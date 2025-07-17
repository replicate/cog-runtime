#!/bin/bash

# Unit tests

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"
port=${PORT:-5000}

cd "$base_dir"

./script/build.sh

name="test-setuid-$(date '+%s')"
docker run -it --rm --detach \
    --name "$name" \
    --entrypoint /bin/bash \
    --publish "$port:$port" \
    --volume "$PWD":/src python:latest \
    -c "find /src/dist -name '*.whl' -exec pip install {} \; && python3 -m cog.server.http --port $port --use-procedure-mode"

sleep 3  # wait for server startup
resp="$(mktemp)"
trap 'rm $resp; docker stop $name' EXIT
curl -fsSL -X POST \
    -H 'Content-Type: application/json' \
    --data '{"context":{"procedure_source_url": "file:///src/python/tests/procedures/setuid", "replicate_api_token": "token"}, "input":{"s":"hello"}}' \
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
