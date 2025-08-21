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

The HTTP server component handles all external communication and process management:

**HTTP routing**: Implements the Cog prediction API with endpoints for predictions, health checks, cancellation, and shutdown. Routes are dynamically configured based on runtime mode (standard vs procedure).

**Process management**: Manages Python runner processes including startup, shutdown, health monitoring, and crash recovery. In procedure mode, can manage multiple isolated runners with automatic eviction policies.

**Request coordination**: Handles request queuing, concurrency limits, and response aggregation. Maps HTTP requests to appropriate Python runners and manages the full request lifecycle.

**File I/O handling**: Manages input file downloads and output file uploads, with support for both local storage and external upload endpoints. Handles path resolution and cleanup automatically.

**IPC coordination**: Receives status updates from Python processes via HTTP and manages bidirectional communication through the filesystem.

### Python model runner (coglet)

The `coglet` component focuses purely on model execution and introspection:

**Model inspection**: Uses AST analysis and runtime introspection to generate OpenAPI schemas from Python predictor code. Supports both class-based and function-based predictors.

**Prediction execution**: Handles synchronous and asynchronous prediction execution with support for streaming outputs via Python generators and async generators.

**Input/output validation**: Validates inputs against generated schemas and normalizes outputs. Supports complex types including paths, files, and custom objects.

**Lifecycle management**: Handles predictor setup, prediction execution, and cleanup with proper exception handling and resource management.

**Context isolation**: Provides per-prediction context isolation for logs, metrics, and other state to prevent cross-contamination between concurrent predictions.

### Request flow architecture

**Standard mode**: Single Python runner handling requests sequentially or with limited concurrency based on predictor capabilities.

**Procedure mode**: Dynamic runner management where the Go server creates Python processes on-demand for different source URLs, with automatic scaling and eviction based on usage patterns.

**Concurrency handling**: The Go server aggregates concurrency limits across all runners and provides global throttling while individual Python processes handle their own internal concurrency based on predictor type (sync vs async).

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

## Contrast with old Cog server

The new architecture addresses several limitations of the original FastAPI-based implementation:

### Performance improvements

**Go HTTP handling**: The Go server can handle much higher request throughput and lower latency compared to Python's uvicorn, especially for health checks and simple requests.

**Process isolation**: Model crashes or hangs no longer affect the HTTP server, providing better availability and faster recovery.

**Concurrent processing**: Better support for concurrent predictions with proper resource accounting and backpressure management.

### Reliability improvements

**Fault tolerance**: Python process crashes are isolated and can be recovered without restarting the entire server.

**Resource management**: Better control over memory usage, file descriptor limits, and process lifecycle.

**Dependency isolation**: Zero Python dependencies in the runtime layer eliminates version conflicts with model requirements.

### Operational improvements

**Multi-tenancy**: Procedure mode allows serving multiple models/procedures from a single server instance with proper isolation.

**Debuggability**: File-based IPC makes it easy to inspect request/response payloads and trace execution flow.

**Resource cleanup**: Automatic cleanup of temporary files, processes, and other resources with proper error handling.

### API compatibility

**Backward compatibility**: Maintains full API compatibility with existing Cog clients while providing performance and reliability improvements.

**Extended features**: Adds procedure mode capabilities while preserving the original single-model deployment pattern.

The old server's single-process architecture required careful management of async/await patterns and could suffer from blocking operations affecting the entire service. The new architecture's process separation eliminates these concerns while providing better scalability and fault isolation.
