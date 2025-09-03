import os

import nox

# Python versions from pyproject.toml
PYTHON_VERSIONS = ["3.9", "3.10", "3.11", "3.12", "3.13"]

# Use uv for faster package installs
nox.options.default_venv_backend = "uv"


@nox.session(python=PYTHON_VERSIONS)
def tests(session):
    """Run tests with pytest-xdist parallelization."""
    session.install(".[test]")
    
    # Pass through any arguments to pytest
    pytest_args = ["-vv", "-n", "auto"] + list(session.posargs)
    
    # Only add -n auto if -n isn't already specified
    if any(arg.startswith("-n") for arg in session.posargs):
        pytest_args = ["-vv"] + list(session.posargs)
    
    session.run("pytest", *pytest_args)


@nox.session(python="3.11")  # Single version for regular testing
def test(session):
    """Run tests on single Python version (faster for development)."""
    session.install(".[test]")
    
    # Pass through any arguments to pytest
    pytest_args = ["-vv", "-n", "auto"] + list(session.posargs)
    
    # Only add -n auto if -n isn't already specified
    if any(arg.startswith("-n") for arg in session.posargs):
        pytest_args = ["-vv"] + list(session.posargs)
    
    session.run("pytest", *pytest_args)


@nox.session(python=None)  # Use current system Python, but create venv
def test_current(session):
    """Run tests using current system Python with isolated venv (for CI)."""
    session.install(".[test]")
    
    # Pass through any arguments to pytest
    pytest_args = ["-vv", "-n", "auto"] + list(session.posargs)
    
    # Only add -n auto if -n isn't already specified
    if any(arg.startswith("-n") for arg in session.posargs):
        pytest_args = ["-vv"] + list(session.posargs)
    
    session.run("pytest", *pytest_args)


@nox.session
def lint(session):
    """Run ruff linting (check mode)."""
    session.install(".[dev]")
    # Check if --fix is in posargs for local dev
    if "--fix" in session.posargs:
        session.run("ruff", "check", "--fix", ".")
    else:
        session.run("ruff", "check", ".")


@nox.session
def format(session):
    """Format code with ruff."""
    session.install(".[dev]")
    # Check mode for CI, format mode for local
    if "--check" in session.posargs:
        session.run("ruff", "format", "--check", ".")
    else:
        session.run("ruff", "format", ".")


@nox.session
def typecheck(session):
    """Run mypy type checking."""
    session.install(".[dev]")
    session.run(
        "mypy", ".", 
        "--exclude", "build",
        "--exclude", "python/tests/cases",
        "--exclude", "python/tests/bad_inputs", 
        "--exclude", "python/tests/bad_predictors",
        "--exclude", "python/tests/runners",
        "--exclude", "python/tests/schemas",
        "--exclude", "python/.*\\.pyi",
        *session.posargs
    )


@nox.session
def check_all(session):
    """Run all checks (lint, format check, typecheck)."""
    session.notify("lint")
    session.notify("format", ["--check"])
    session.notify("typecheck")