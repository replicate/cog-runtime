# Cog Runtime

New implementation for Cog's production runtime component, which is responsible for:

* Cog HTTP server
* Input and output schema validation
* Model execution

Cog HTTP server is rewritten in Go for better performance, reliability, concurrency and isolation.

Schema validation and model execution were rewritten in pure Python for simplicity, better error handling and reduced risk of dependency conflicts.

## Cog HTTP server

This is the Go HTTP server that:

* Manages the Python model runner process
* Handles HTTP requests
* Downloads input files and uploads output files
* Manages async and concurrency predictions
* Makes webhook callbacks
* Logging and health check of Python runner process
* Communicates with the Python runner via a mix of Unix signals, HTTP, and JSON files

## `coglet`

Python model runner that:

* Is source compatible with existing Cog API
* Has zero Python dependency to minimize risk of interfering with model code
* Inspects Python predictor code for input and output schema
* Invokes `setup()` and `predict()` methods

[Cog]: <https://github.com/replicate/cog>
