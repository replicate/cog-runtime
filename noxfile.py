import nox

# Python versions from pyproject.toml
PYTHON_VERSIONS = ['3.9', '3.10', '3.11', '3.12', '3.13']


def _generate_stubs():
    """Helper function to generate pyright stubs."""
    import os
    import shutil
    import subprocess

    # Check if npx is available
    try:
        subprocess.run(['npx', '--version'], capture_output=True, check=True)
    except (subprocess.CalledProcessError, FileNotFoundError):
        raise RuntimeError(
            'npx not found. Please install Node.js to generate type stubs.\n'
            'Visit: https://nodejs.org/ or use your package manager.'
        )

    # Remove existing stubs to avoid duplication
    subprocess.run(['find', 'python', '-name', '*.pyi', '-type', 'f', '-delete'])

    # Generate stubs using npx pyright
    env = os.environ.copy()
    env['PYTHONPATH'] = 'python'

    try:
        subprocess.run(
            ['npx', '-y', 'pyright', '--createstub', 'coglet'], env=env, check=True
        )
    except subprocess.CalledProcessError:
        print('Warning: coglet stub creation may have failed')

    try:
        subprocess.run(
            ['npx', '-y', 'pyright', '--createstub', 'cog'], env=env, check=True
        )
    except subprocess.CalledProcessError:
        print('Warning: cog stub creation may have failed')

    # Move stubs from typings/ to alongside source
    if os.path.exists('typings'):
        shutil.copytree('typings', 'python', dirs_exist_ok=True)
        shutil.rmtree('typings')


# Use uv for faster package installs
nox.options.default_venv_backend = 'uv'


@nox.session(python=PYTHON_VERSIONS)
def tests(session):
    """Run tests with pytest-xdist parallelization."""
    session.install('.[test]')

    # Pass through any arguments to pytest
    pytest_args = ['-vv', '-n', 'auto'] + list(session.posargs)

    # Only add -n auto if -n isn't already specified
    if any(arg.startswith('-n') for arg in session.posargs):
        pytest_args = ['-vv'] + list(session.posargs)

    session.run('pytest', *pytest_args)


@nox.session(python='3.11')  # Single version for regular testing
def test(session):
    """Run tests on single Python version (faster for development)."""
    session.install('.[test]')

    # Pass through any arguments to pytest
    pytest_args = ['-vv', '-n', 'auto'] + list(session.posargs)

    # Only add -n auto if -n isn't already specified
    if any(arg.startswith('-n') for arg in session.posargs):
        pytest_args = ['-vv'] + list(session.posargs)

    session.run('pytest', *pytest_args)


@nox.session(python=None)  # Use current system Python, but create venv
def test_current(session):
    """Run tests using current system Python with isolated venv (for CI)."""
    session.install('.[test]')

    # Pass through any arguments to pytest
    pytest_args = ['-vv', '-n', 'auto'] + list(session.posargs)

    # Only add -n auto if -n isn't already specified
    if any(arg.startswith('-n') for arg in session.posargs):
        pytest_args = ['-vv'] + list(session.posargs)

    session.run('pytest', *pytest_args)


@nox.session
def lint(session):
    """Run ruff linting (check mode)."""
    session.install('.[dev]')
    # Check if --fix is in posargs for local dev
    check_only = '--fix' not in session.posargs
    session.log(f'running lint... {"[check_only]" if check_only else "[auto_fix]"}')
    if '--fix' in session.posargs:
        session.run('ruff', 'check', '--fix', '.')
    else:
        session.run('ruff', 'check', '.')


@nox.session
def format(session):
    """Format code with ruff."""
    session.install('.[dev]')
    # Check mode for CI, format mode for local
    if '--check' in session.posargs:
        session.run('ruff', 'format', '--check', '.')
    else:
        session.run('ruff', 'format', '.')


@nox.session
def stubs(session):
    """Generate pyright stubs."""
    session.log('stubbing python modules...')
    try:
        _generate_stubs()
        session.log('python module stubs generated successfully')
    except RuntimeError as e:
        session.error(str(e))


@nox.session
def typecheck(session):
    """Run mypy type checking."""
    import os

    session.log('running typecheck...')
    # Only generate stubs if not in CI (CI validates existing stubs separately)
    if not os.environ.get('CI'):
        session.log('auto-generating python module stubs...')
        # Run stubs generation inline instead of separate session
        _generate_stubs()
        session.log('done auto-generating python module stubs...')

    # Install dev deps (mypy) + test deps (pytest) + provided deps (pydantic)
    session.install('.[dev,test,provided]')
    session.run(
        'mypy',
        '.',
        '--exclude',
        'build',
        '--exclude',
        'python/tests/cases',
        '--exclude',
        'python/tests/bad_inputs',
        '--exclude',
        'python/tests/bad_predictors',
        '--exclude',
        'python/tests/runners',
        '--exclude',
        'python/tests/schemas',
        '--exclude',
        'python/.*\\.pyi',
        *session.posargs,
    )


@nox.session
def check_all(session):
    """Run all checks (lint, format check, typecheck)."""
    session.notify('lint')
    session.notify('format', ['--check'])
    session.notify('typecheck')
