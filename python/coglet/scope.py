import contextvars
from collections import defaultdict
from typing import Any, Callable, Dict, Optional

ctx_pid: contextvars.ContextVar[Optional[str]] = contextvars.ContextVar(
    'pid', default=None
)
metrics: Dict[str, Dict[str, Any]] = defaultdict(dict)


class Scope:
    def __init__(self, pid: str):
        self.pid = pid

    def record_metric(self, name: str, value: Any) -> None:
        metrics[self.pid][name] = value


def current_scope() -> Scope:
    pid = ctx_pid.get()
    assert pid is not None
    return Scope(pid)


def ctx_write(write_fn) -> Callable[[str], int]:
    buf: Dict[str, str] = {}

    def _write(s: str) -> int:
        if len(s) == 0:
            return 0
        pid = ctx_pid.get()
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
