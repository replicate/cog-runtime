import argparse
import asyncio
import contextvars
import importlib
import json
import logging
import os
import os.path
import sys
import time
from typing import Callable, Dict, Optional, Tuple

from coglet import file_runner


def pre_setup(logger: logging.Logger, working_dir: str) -> Optional[Tuple[str, str]]:
    if os.environ.get('R8_TORCH_VERSION') is not None:
        logger.info('eagerly importing torch')
        importlib.import_module('torch')

    # Cog server waits until user files become available and passes config to Python runner
    conf_file = os.path.join(working_dir, 'config.json')
    elapsed = 0.0
    timeout = 60.0
    while elapsed < timeout:
        if os.path.exists(conf_file):
            logger.info(f'config file found after {elapsed:.2f}s: {conf_file}')
            with open(conf_file, 'r') as f:
                conf = json.load(f)
                os.unlink(conf_file)
            module_name = conf['module_name']
            class_name = conf['class_name']

            # Add user venv to PYTHONPATH
            pv = f'python{sys.version_info.major}.{sys.version_info.minor}'
            venv = os.path.join('/', 'root', '.venv', 'lib', pv, 'site-packages')
            if venv is not None and venv not in sys.path and os.path.exists(venv):
                logger.info(f'adding venv to PYTHONPATH: {venv}')
                sys.path.append(venv)
                # In case the model forks Python interpreter
                os.environ['PYTHONPATH'] = ':'.join(sys.path)
            return module_name, class_name
        time.sleep(0.01)
        elapsed += 0.01

    logger.error(f'config file not found after {timeout:.2f}s: {conf_file}')
    return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        '--working-dir', metavar='DIR', required=True, help='working directory'
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

    tup = pre_setup(logger, args.working_dir)
    if tup is None:
        return -1
    module_name, class_name = tup

    return asyncio.run(
        file_runner.FileRunner(
            logger=logger,
            working_dir=args.working_dir,
            module_name=module_name,
            class_name=class_name,
            ctx_pid=_ctx_pid,
        ).start()
    )


if __name__ == '__main__':
    sys.exit(main())
