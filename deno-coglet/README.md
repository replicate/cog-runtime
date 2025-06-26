# Deno Coglet

A Deno/TypeScript implementation of the Cog runtime interface that allows JavaScript/TypeScript predictors to work with the Cog Go runner.

## Overview

This implementation provides the same file-based IPC interface as the Python coglet, enabling the Go runner to manage JavaScript/TypeScript prediction functions and classes.

## Architecture

The communication protocol uses:
- **File-based IPC** for exchanging requests and responses
- **HTTP status updates** to notify the Go runner of state changes
- **Formatted logging** for proper log routing

## Usage

### Running the Coglet

```bash
deno run --allow-read --allow-write --allow-net --allow-env \
  coglet.ts \
  --ipc-url http://localhost:8080 \
  --working-dir /tmp/work
```

### Creating a Predictor

You can create predictors as functions or classes:

```typescript
// Function predictor
export function myPredictor(input: { text: string }) {
  return { result: input.text.toUpperCase() };
}

// Class predictor with setup
export class MyPredictor {
  private model: any;
  
  async setup() {
    // Initialize your model here
    this.model = await loadModel();
  }
  
  async predict(input: { prompt: string }) {
    return await this.model.generate(input.prompt);
  }
}
```

### Integration with Go Runner

To use this with the Go runner, you would need to modify the runner to use Deno instead of Python:

```go
// In runner.go
args := []string{
    "run",
    "--allow-read", "--allow-write", "--allow-net", "--allow-env",
    "/path/to/coglet.ts",
    "--ipc-url", ipcUrl,
    "--working-dir", workingDir,
}
cmd := exec.Command("deno", args...)
```

## File Interface

- `config.json` - Initial configuration from Go
- `request-{id}.json` - Prediction requests
- `response-{id}-{epoch}.json` - Prediction responses
- `cancel-{id}` - Cancel specific prediction
- `stop` - Shutdown signal
- `setup_result.json` - Setup status
- `openapi.json` - API schema

## Testing

Run the test script to see it in action:

```bash
./test_runner.sh
```

This will start a mock IPC server, run the coglet with an example predictor, and send a test prediction request.