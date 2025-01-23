import argparse
import asyncio
import contextvars
import importlib
import logging
import os
import os.path
import sys
import time
from typing import Callable, Dict, Optional

from coglet import file_runner


def pre_setup(logger: logging.Logger) -> bool:
    if os.environ.get('R8_TORCH_VERSION') is not None:
        logger.info('eagerly importing torch')
        importlib.import_module('torch')

    wait_file = os.environ.get('COG_WAIT_FILE')
    if wait_file is None:
        return True
    elapsed = 0.0
    timeout = 60.0
    while elapsed < timeout:
        if os.path.exists(wait_file):
            logger.info(f'wait file found after {elapsed:.2f}s: {wait_file}')
            pyenv_path = os.environ.get('COG_PYENV_PATH')
            if pyenv_path is not None:
                p = os.path.join(
                    pyenv_path,
                    'lib',
                    f'python{sys.version_info.major}.f{sys.version_info.minor}',
                    'site-packages',
                )
                if p not in sys.path:
                    logger.info(f'adding pyenv to PYTHONPATH: {p}')
                    sys.path.append(p)
                    # In case the model forks Python interpreter
                    os.environ['PYTHONPATH'] = ':'.join(sys.path)
            return True
        time.sleep(0.01)
        elapsed += 0.01
    logger.error(f'wait file not found after {timeout:.2f}s: {wait_file}')
    return False


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        '--working-dir', metavar='DIR', required=True, help='working directory'
    )
    parser.add_argument(
        '--module-name', metavar='NAME', required=True, help='Python module name'
    )
    parser.add_argument(
        '--class-name', metavar='NAME', required=True, help='Python class name'
    )

    logger = logging.getLogger('coglet')
    logger.setLevel(logging.INFO)
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(
        logging.Formatter(
            '%(asctime)s\t%(levelname)s\t[%(name)s]\t%(filename)s:%(lineno)d\t%(message)s'
        )
    )
    logger.addHandler(handler)

    _ctx_pid: contextvars.ContextVar[Optional[str]] = contextvars.ContextVar(
        'pid', default=None
    )

    _stdout_write = sys.stdout.write
    _stderr_write = sys.stderr.write

    def _ctx_write(write_fn) -> Callable[[str], int]:
        buf: Dict[str, str] = {}

        def _write(s: str) -> int:
            if len(s) == 0:
                return 0
            pid = _ctx_pid.get()
            prefix = f'[pid={pid}] ' if pid is not None else ''
            if pid is None:
                pid = 'logger'
            n = 0
            s = s.replace('\r', '\n')
            if s[-1] == '\n':
                b = buf.pop(pid, '')
                out = b + s[:-1].replace('\n', f'\n{prefix}') + '\n'
                n += write_fn(out)
                buf[pid] = prefix
            elif '\n' in s:
                b = buf.pop(pid, '')
                lines = s.replace('\n', f'\n{prefix}').split('\n')
                n += write_fn(b + lines[0] + '\n')
                for line in lines[1:-1]:
                    n += write_fn(b + line + '\n')
                buf[pid] = lines[-1]
            else:
                if pid not in buf:
                    buf[pid] = prefix
                buf[pid] += s.replace('\n', f'\n{prefix}')
            return n

        return _write

    sys.stdout.write = _ctx_write(_stdout_write)  # type: ignore
    sys.stderr.write = _ctx_write(_stderr_write)  # type: ignore

    args = parser.parse_args()

    if not pre_setup(logger):
        return -1

    return asyncio.run(
        file_runner.FileRunner(
            logger=logger,
            working_dir=args.working_dir,
            module_name=args.module_name,
            class_name=args.class_name,
            ctx_pid=_ctx_pid,
        ).start()
    )


if __name__ == '__main__':
    sys.exit(main())
