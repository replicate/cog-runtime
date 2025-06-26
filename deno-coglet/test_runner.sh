#!/bin/bash

# Test script for Deno coglet

echo "Creating test working directory..."
WORKING_DIR=$(mktemp -d)
echo "Working directory: $WORKING_DIR"

# Start a simple HTTP server to receive IPC status updates
echo "Starting IPC server..."
deno run --allow-net - <<'EOF' &
const server = Deno.serve({ port: 8080 }, (req) => {
  const body = req.json();
  body.then(data => {
    console.log(`[IPC Server] Received status: ${data.status}`);
  });
  return new Response("OK");
});
console.log("[IPC Server] Listening on http://localhost:8080");
EOF
IPC_PID=$!

sleep 2

# Start the coglet
echo "Starting Deno coglet..."
deno run --allow-read --allow-write --allow-net --allow-env \
  coglet.ts \
  --ipc-url http://localhost:8080 \
  --working-dir "$WORKING_DIR" &
COGLET_PID=$!

# Wait a bit for coglet to start
sleep 1

# Write config
echo "Writing config..."
cat > "$WORKING_DIR/config.json" <<EOF
{
  "module_name": "./example_predictor.ts",
  "predictor_name": "simplePredictFunction",
  "max_concurrency": 2
}
EOF

# Wait for setup
echo "Waiting for setup..."
sleep 2

# Send a prediction request
echo "Sending prediction request..."
cat > "$WORKING_DIR/request-test123.json" <<EOF
{
  "id": "test123",
  "input": {
    "text": "Hello, Deno coglet!",
    "multiplier": 3
  },
  "created_at": "$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)"
}
EOF

# Monitor for response
echo "Waiting for response..."
for i in {1..30}; do
  if ls "$WORKING_DIR"/response-test123-*.json 2>/dev/null; then
    echo "Response received:"
    cat "$WORKING_DIR"/response-test123-*.json | jq .
    break
  fi
  sleep 0.5
done

# Cleanup
echo "Cleaning up..."
echo "" > "$WORKING_DIR/stop"
sleep 1
kill $COGLET_PID $IPC_PID 2>/dev/null
rm -rf "$WORKING_DIR"

echo "Test complete!"