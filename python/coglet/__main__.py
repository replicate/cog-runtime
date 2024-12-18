import argparse
import asyncio
import contextvars
import logging
import sys
from typing import Callable, Dict, Optional

from coglet import file_runner


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
            pid = _ctx_pid.get()
            prefix = f'[pid={pid}] ' if pid is not None else ''
            if pid is None:
                pid = 'logger'
            n = 0
            if s[-1] == '\n':
                b = buf.pop(pid, '')
                out = b + s[:-1].replace('\n', f'\n{prefix}') + '\n'
                n += write_fn(out)
                buf[pid] = prefix
            else:
                if pid not in buf:
                    buf[pid] = prefix
                buf[pid] += s.replace('\n', f'\n{prefix}')
            return n

        return _write

    sys.stdout.write = _ctx_write(_stdout_write)  # type: ignore
    sys.stderr.write = _ctx_write(_stderr_write)  # type: ignore

    args = parser.parse_args()

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
