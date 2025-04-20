import contextlib

from datetime import datetime, timezone

from coglet import api


def now_iso() -> str:
    # Go: time.Now().UTC().Format("2006-01-02T15:04:05.999999-07:00")
    return datetime.now(timezone.utc).isoformat()


# Encode JSON for file_runner output
def output_json(obj):
    tpe = type(obj)
    if tpe is api.Path:
        # Prefix protocol for uploader
        return f'file://{obj.absolute()}'
    elif tpe is api.Secret:
        # Encode Secret('foobar') as '**********'
        return str(obj)
    else:
        raise TypeError(f'Object of type {tpe} is not JSON serializable')


# Encode JSON for Open API schema
def schema_json(obj):
    tpe = type(obj)
    if tpe is api.Path:
        # Encode Path('x/y/z') as 'x/y/z'
        return str(obj)
    elif tpe is api.Secret:
        # Encode Secret('foobar') as '**********'
        return str(obj)
    else:
        raise TypeError(f'Object of type {tpe} is not JSON serializable')


if hasattr(contextlib, "chdir"):
    chdir = contextlib.chdir
else:
    import os

    class chdir(contextlib.AbstractContextManager):
        """Non thread-safe context manager to change the current working directory."""

        def __init__(self, path):
            self.path = path
            self._old_cwd = []

        def __enter__(self):
            self._old_cwd.append(os.getcwd())
            os.chdir(self.path)

        def __exit__(self, *excinfo):
            os.chdir(self._old_cwd.pop())
