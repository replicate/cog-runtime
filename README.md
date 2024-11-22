# Cog Runtime

Alt-core [Cog] runtime implementation.

The original [Cog] seeks to be a great developer tool and also an arguably great
production runtime. Cog runtime is focused on being a fantastic production runtime _only_.

How is `cog-runtime` formed?

```mermaid
sequenceDiagram
    participant r8
    participant server as coggo-server
    participant runner as coglet
    participant predictor as Predictor
    r8->>server: Boot
    activate server
    server->>runner: exec("python3", ...)
    activate runner
    runner<<->>predictor: setup()
    activate predictor
    runner--)server: SIGHUP (output)
    runner--)server: SIGUSR1 (ready)
    server->>runner: read("setup-result.json")
    r8->>server: GET /health-check
    r8->>server: POST /predictions
    server->>runner: write("request-{id}.json")
    runner--)server: SIGUSR2 (busy)
    runner<<->>predictor: predict()
    deactivate predictor
    runner--)server: SIGHUP (output)
    runner--)server: SIGUSR1 (ready)
    server->>runner: read("response-{id}.json")
    deactivate runner
    server->>r8: POST /webhook
    deactivate server
```

This sequence is simplified, but the rough idea is that the Replicate platform (`r8`)
depends on `cog-server` to provide an HTTP API in front of a `coglet`
that communicates via files and signals.

## `cog-server`

Go-based HTTP server that known how to spawn and communicate with
`coglet`.

## `coglet`

Python-based model runner with zero dependencies outside of the standard library.
The same in-process API provided by [Cog] is avaaliable, e.g.:

```python
from cog import BasePredictor, Input

class MyPredictor(BasePredictor):
    def predict(self, n=Input(default="how many", ge=1, le=100)) -> str:
        return "ok" * n
```

In addition to simple cases like the above, the runner is async by default and supports
continuous batching.

Communication with the `cog-server` parent process is managed via input and output files
and the following signals:

- `SIGUSR1` model is ready
- `SIGUSR2` model is busy
- `SIGHUP` output is available

[Cog]: <https://github.com/replicate/cog>
