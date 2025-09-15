# Architecture

This document describes the key architectural components and decisions for the new Cog runtime system.

## Overview

A complete rewrite of Cog's production runtime that separates concerns into two main components:

- **Go HTTP server**: High-performance server handling HTTP requests, managing Python processes, and coordinating execution
- **Python model runner (`coglet`)**: Zero-dependency Python component responsible for model execution, schema inspection, and prediction handling

This hybrid architecture combines Go's performance advantages with Python's machine learning ecosystem compatibility, while providing better isolation, concurrency, and reliability compared to the previous pure-Python implementation.

## Key architectural decisions

**Process separation**: Unlike the old Cog server that ran everything in a single Python process, the new architecture uses separate processes for the HTTP server (Go) and model execution (Python). This provides better fault isolation, resource management, and allows the HTTP layer to remain responsive even during heavy model execution.

**Zero-dependency Python runtime**: The `coglet` component has zero external Python dependencies to avoid conflicts with model dependencies. This contrasts with the old server's FastAPI-based implementation that required specific versions of web framework dependencies.

**File-based IPC**: Communication between Go and Python processes happens through JSON files and HTTP requests rather than shared memory or pipes. This provides better debuggability and crash resilience.

**Procedure mode**: A multi-tenant mode that allows running multiple isolated Python processes for different models/procedures, with automatic lifecycle management and resource allocation.

## Core components

### Go HTTP server

The HTTP server component is organized into focused packages with clear separation of concerns:

#### `internal/server` - HTTP API Layer
**HTTP routing**: Implements the Cog prediction API with endpoints for predictions, health checks, cancellation, and shutdown. Routes are dynamically configured based on runtime mode (standard vs procedure).

**Request handling**: Processes incoming HTTP requests, validates payloads, and coordinates with the runner management layer. Handles both synchronous and asynchronous prediction workflows.

**Response management**: Aggregates results from runner layer and formats responses according to API specifications. Manages streaming responses and webhook notifications.

#### `internal/runner` - Process Management Layer
**Centralized runner management**: The `Manager` component handles all Python process lifecycle including startup, shutdown, health monitoring, and crash recovery. Provides slot-based concurrency control and automatic resource cleanup.

**Individual runner management**: The `Runner` component manages individual Python processes with proper context cancellation, log accumulation, and per-prediction response tracking.

**Procedure support**: Dynamic runner creation for different source URLs with automatic eviction policies. Handles isolation requirements and resource allocation in multi-tenant scenarios.

**Configuration management**: Handles cog.yaml parsing, environment setup, and runtime configuration for both standard and procedure modes.

#### `internal/webhook` - Webhook Delivery
**Webhook coordination**: Manages webhook delivery with deduplication, retry logic, and proper event filtering. Uses atomic operations to prevent duplicate terminal webhooks.

**Event tracking**: Tracks webhook events per prediction with proper timing and log accumulation to ensure complete notification delivery.

#### `internal/service` - Application Lifecycle
**Service coordination**: Manages overall application lifecycle including graceful shutdown, signal handling, and component initialization.

**Configuration integration**: Bridges CLI configuration with internal component configuration and handles service-level concerns like working directory management.

### Python model runner (coglet)

The `coglet` component focuses purely on model execution and introspection:

**Model inspection**: Uses AST analysis and runtime introspection to generate OpenAPI schemas from Python predictor code. Supports both class-based and function-based predictors.

**Prediction execution**: Handles synchronous and asynchronous prediction execution with support for streaming outputs via Python generators and async generators.

**Input/output validation**: Validates inputs against generated schemas and normalizes outputs. Supports complex types including paths, files, and custom objects.

**Lifecycle management**: Handles predictor setup, prediction execution, and cleanup with proper exception handling and resource management.

**Context isolation**: Provides per-prediction context isolation for logs, metrics, and other state to prevent cross-contamination between concurrent predictions.

### Request flow architecture

The architecture provides clean separation between HTTP handling, runner management, and process execution:

#### Request Processing Flow

1. **HTTP Request**: `internal/server` receives and validates incoming requests
2. **Runner Assignment**: `internal/runner/Manager` assigns requests to available runners using slot-based concurrency control
3. **Process Execution**: `internal/runner/Runner` manages individual Python process interaction via file-based IPC
4. **Response Tracking**: Per-prediction watchers monitor Python process responses and handle log accumulation
5. **Webhook Delivery**: `internal/webhook` manages asynchronous webhook notifications with deduplication
6. **HTTP Response**: `internal/server` formats and returns final responses to clients

#### Execution Modes

**Standard mode**: Single Python runner managed by the system with configurable concurrency based on predictor capabilities. The Manager creates and maintains one long-lived runner process.

**Procedure mode**: Dynamic runner management where the Manager creates Python processes on-demand for different source URLs. Implements LRU eviction, automatic scaling, and resource isolation between procedures.

**Concurrency handling**: The Manager provides global slot-based concurrency control while individual Runners handle per-process concurrency limits. Atomic operations ensure safe concurrent access to shared state.

## Communication patterns

### HTTP API endpoints

The server exposes a RESTful API compatible with the original Cog specification:

**Prediction endpoints**: `POST /predictions` for new predictions, `PUT /predictions/{id}` for idempotent creation, with support for both synchronous and asynchronous processing modes.

**Management endpoints**: Health checks, OpenAPI schema retrieval, graceful shutdown, and prediction cancellation.

**Procedure endpoints**: In procedure mode, uses `/procedures` paths instead of `/predictions` to distinguish the multi-tenant execution model.

### Inter-process communication

**Status updates**: Python processes send HTTP requests to the Go server to report status changes (ready, busy, output available).

**File-based messaging**: Request/response payloads are exchanged via JSON files in a shared working directory, providing crash resilience and debuggability.

**Signal handling**: Limited use of Unix signals for cancellation in non-async predictors, with file-based cancellation for async predictors to avoid signal handling complexity.

### File management

**Working directories**: Each Python runner gets an isolated working directory for request/response files, temporary storage, and IPC coordination.

**Input handling**: The Go server downloads input URLs to local files and updates request payloads with local paths before sending to Python.

**Output processing**: Python runners write outputs to files when needed, and the Go server handles upload/base64 encoding based on client preferences.

## Architecture benefits

The hybrid Go/Python architecture provides several key advantages:

### Performance characteristics

**Go HTTP handling**: The Go server provides high request throughput and low latency, especially for health checks and management requests.

**Process isolation**: Model crashes or hangs do not affect the HTTP server, providing better availability and faster recovery.

**Concurrent processing**: Supports concurrent predictions with proper resource accounting and backpressure management through slot-based concurrency control.

### Reliability features

**Fault tolerance**: Python process crashes are isolated and recovered without affecting other operations or requiring server restart.

**Resource management**: Provides precise control over memory usage, file descriptor limits, and process lifecycle with automatic cleanup.

**Dependency isolation**: Zero Python dependencies in the runtime layer eliminates version conflicts with model requirements.

### Operational capabilities

**Multi-tenancy**: Procedure mode serves multiple models/procedures from a single server instance with proper isolation and resource allocation.

**Debuggability**: File-based IPC enables easy inspection of request/response payloads and execution flow tracing.

**Resource cleanup**: Automatic cleanup of temporary files, processes, and other resources with comprehensive error handling.

### API design

**Client compatibility**: Maintains full API compatibility with existing Cog clients while providing enhanced performance and reliability.

**Flexible deployment**: Supports both single-model deployment patterns and multi-tenant procedure mode from the same codebase.

The process separation architecture eliminates concerns around blocking operations affecting the entire service while providing better scalability and fault isolation through clear component boundaries.
