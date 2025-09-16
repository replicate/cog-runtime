#!/bin/bash

# Test UID-based cleanup in /tmp - verifies files owned by isolated UIDs are cleaned up
# Uses --one-shot mode to guarantee cleanup happens after each prediction

set -euo pipefail

base_dir="$(git rev-parse --show-toplevel)"
port=${PORT:-5000}

cd "$base_dir"

./script/build.sh

name="test-setuid-cleanup-$(date '+%s')"

# Script to run in container
read -r -d '' SCRIPT << EOF || :
set -e

LOG_LEVEL=debug
export LOG_LEVEL

# Copy test procedures  
cp -r /src/python/tests/procedures /
chmod 700 /procedures/cleanup
chmod 600 /procedures/cleanup/*

# Install cog-runtime
find /src/dist -name '*.whl' -exec pip install {} \;

# Start server in one-shot mode
python3 -m cog.server.http \
    --port $port \
    --use-procedure-mode \
    --one-shot \
    --max-runners 1
EOF

# Start Docker container
docker run -it --detach \
    --name "$name" \
    --entrypoint /bin/bash \
    --publish "$port:$port" \
    --volume "$PWD":/src python:latest \
    -c "$SCRIPT"

trap 'docker stop $name' EXIT

# Wait for server to be ready
sleep 3

wait_for_ready() {
    for i in {1..10}; do
        if curl -fsSL "http://localhost:$port/health-check" 2>/dev/null | grep -q "READY"; then
            return 0
        fi
        sleep 1
    done
    return 1
}

echo "Waiting for initial ready state..."
wait_for_ready || { echo "Server never became ready"; exit 1; }

# Run first prediction
echo "Running prediction 1..."
resp1=$(mktemp)
curl -fsSL -X POST \
    -H 'Content-Type: application/json' \
    --data '{"context":{"procedure_source_url": "file:///procedures/cleanup", "replicate_api_token": "test-token"}, "input":{"test_id": 1}}' \
    "http://localhost:$port/procedures" > "$resp1"

pred1_id=$(jq -r '.id' < "$resp1")
echo "Prediction 1 ID: $pred1_id"

# Wait for prediction to complete and server to be ready again
echo "Waiting for prediction 1 to complete and cleanup..."
sleep 2
wait_for_ready || { echo "Server never became ready after prediction 1"; exit 1; }

# Check if files from first runner were cleaned up
echo "Checking cleanup after prediction 1..."
remaining_files=$(docker exec "$name" find /tmp -name "cleanup-*" -type f 2>/dev/null | wc -l)
if [ "$remaining_files" -ne 0 ]; then
    echo "FAILED: Found $remaining_files files remaining after cleanup"
    docker exec "$name" find /tmp -name "cleanup-*" -type f 2>/dev/null || echo "No files found"
    echo "Docker logs:"
    docker logs "$name"
    exit 1
fi
echo "SUCCESS: All files from prediction 1 were cleaned up"

# Run second prediction to verify it works again
echo "Running prediction 2..."
resp2=$(mktemp)
curl -fsSL -X POST \
    -H 'Content-Type: application/json' \
    --data '{"context":{"procedure_source_url": "file:///procedures/cleanup", "replicate_api_token": "test-token"}, "input":{"test_id": 2}}' \
    "http://localhost:$port/procedures" > "$resp2"

pred2_id=$(jq -r '.id' < "$resp2")
echo "Prediction 2 ID: $pred2_id"

# Wait and check cleanup again
echo "Waiting for prediction 2 to complete and cleanup..."
sleep 2
wait_for_ready || { echo "Server never became ready after prediction 2"; exit 1; }

echo "Checking cleanup after prediction 2..."
remaining_files=$(docker exec "$name" find /tmp -name "cleanup-*" -type f 2>/dev/null | wc -l)
if [ "$remaining_files" -ne 0 ]; then
    echo "FAILED: Found $remaining_files files remaining after second cleanup"
    docker exec "$name" find /tmp -name "cleanup-*" -type f 2>/dev/null || echo "No files found"
    echo "Docker logs:"
    docker logs "$name"
    exit 1
fi

echo "SUCCESS: All files from prediction 2 were cleaned up"
echo "Docker logs:"
docker logs "$name"
echo "CLEANUP TEST PASSED"

rm "$resp1" "$resp2"