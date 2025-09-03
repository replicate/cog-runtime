#!/bin/bash

# Common functions for scripts

run_nox() {
    if [[ -n "${CI:-}" ]]; then
        # In CI, use uv tool run nox (no venv setup needed)
        uv tool run nox "$@"
    else
        # Local dev, use nox from .venv
        if [ ! -x ".venv/bin/nox" ]; then
            echo "Error: nox not found in .venv/bin/"
            echo "Run: uv pip install -e \".[dev]\" to install nox"
            exit 1
        fi
        .venv/bin/nox "$@"
    fi
}