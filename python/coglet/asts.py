import ast
import sys
from typing import Callable, List


def print_error(
    file: str, lines: List[str], lineno: int, col_offset: int, msg: str, help: str
) -> None:
    start = max(0, lineno - 2)
    end = min(len(lines), lineno + 2)
    w = len(str(end))
    print(f'{file}:{lineno}:{col_offset}: {msg}', file=sys.stderr)
    for i in range(start, end + 1):
        print(f'%-{w}d | %s' % (i, lines[i]), file=sys.stderr)
        if i == lineno:
            print('%s | %s^' % (' ' * w, ' ' * col_offset), file=sys.stderr)
    print('%s = help: %s' % (' ' * w, help), file=sys.stderr)
    print('', file=sys.stderr)


def visit(
    root: ast.AST,
    file: str,
    lines: List[str],
    f: Callable[[ast.AST, str, List[str]], bool],
) -> bool:
    b = False
    for node in ast.iter_child_nodes(root):
        b = b | f(node, file, lines)
        b = b | visit(node, file, lines, f)
    return b


def inspect_optional(node: ast.AST, file: str, lines: List[str]) -> bool:
    if type(node) is not ast.FunctionDef:
        return False

    if node.name != 'predict':
        return False

    b = False
    n = len(node.args.defaults)
    for a, d in zip(node.args.args[-n:], node.args.defaults):
        # <name>: <type>
        if type(a.annotation) is not ast.Name:
            continue
        # <name>: <type> = Input(...)
        if (
            type(d) is not ast.Call
            or type(d.func) is not ast.Name
            or d.func.id != 'Input'
        ):
            continue
        # <name>: <type> = Input(default=..., ...)
        kws = [kw for kw in d.keywords if kw.arg == 'default']
        if len(kws) != 1:
            continue
        if type(kws[0].value) is not ast.Constant or kws[0].value.value is not None:
            continue
        print_error(
            file,
            lines,
            kws[0].lineno,
            kws[0].col_offset,
            'input with default=None must be Optional',
            f'Change input type to `{a.arg}: Optional[{ast.unparse(a.annotation)}]` instead',
        )
        b = True
    return b


def inspect(file: str):
    with open(file, 'r') as f:
        content = f.read()

    root = ast.parse(content)
    # line numbers are 1-indexed
    lines = [''] + content.splitlines()

    b = visit(root, file, lines, inspect_optional)
    if b:
        raise AssertionError('input with default=None must be Optional')
