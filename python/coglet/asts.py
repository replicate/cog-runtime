import ast
import sys
from typing import Callable, List


def print_err(lines: List[str]) -> None:
    for line in lines:
        print(line, file=sys.stderr)


def print_lines(lines: List[str], start: int, end: int) -> None:
    w = len(str(end)) + 1
    print_err([f'%-{w}d | %s' % (i, lines[i]) for i in range(start, end + 1)])


def visit(
    root: ast.AST, lines: List[str], f: Callable[[ast.AST, List[str]], bool]
) -> bool:
    b = False
    for node in ast.iter_child_nodes(root):
        b = b | f(node, lines)
        b = b | visit(node, lines, f)
    return b


def inspect_optional(node: ast.AST, lines: List[str]) -> bool:
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
        print_lines(lines, a.annotation.lineno, kws[0].lineno)
        # print(d, ast.unparse(d))
        b = True
    return b


def inspect(file: str):
    with open(file, 'r') as f:
        content = f.read()

    root = ast.parse(content)
    # line numbers are 1-indexed
    lines = [''] + content.splitlines()

    b = visit(root, lines, inspect_optional)
    if b:
        print_err(
            [
                '',
                'Default value of None without explicit Optional type hint is ambiguous',
                'Declare input type as Optional instead, for example:',
                '-    prompt: str=Input(description="prompt", default=None)  # None is not str',
                '+    prompt: Optional[str]=Input(description="prompt")      # Optional implies default=None',
            ]
        )
        raise AssertionError('input type must be Optional for None default value')
