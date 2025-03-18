import dataclasses
import typing
from datetime import datetime, timezone
from typing import Any, Optional, Tuple

from coglet import adt, api


def check_cog_type(tpe: type) -> Tuple[adt.Type, bool, Optional[api.Coder]]:
    if typing.get_origin(tpe) is list:
        t_args = typing.get_args(tpe)
        assert len(t_args) == 1, 'list must have one type argument'
        elem_t = t_args[0]
        is_list = True
    else:
        elem_t = tpe
        is_list = False
    cog_t = adt.PYTHON_TO_COG.get(elem_t)
    coder = None
    if cog_t is None and dataclasses.is_dataclass(elem_t):
        cog_t = adt.Type.CUSTOM
        coder = api.DataclassCoder(elem_t)  # type: ignore
    assert cog_t is not None, f'unsupported Cog type {elem_t}'
    return cog_t, is_list, coder


def check_value(expected: adt.Type, value: Any, coder: Optional[api.Coder]) -> bool:
    cog_t = adt.PYTHON_TO_COG.get(type(value))
    if cog_t is None:
        # Unknown Python type, must be custom type with coder
        return expected is adt.Type.CUSTOM and coder is not None
    elif expected is adt.Type.FLOAT:
        return cog_t in adt.NUMERIC_TYPES
    elif expected is adt.Type.PATH:
        return cog_t in adt.PATH_TYPES
    elif expected is adt.Type.SECRET:
        return cog_t in adt.SECRET_TYPES
    else:
        return cog_t is expected


def json_value(expected: adt.Type, value: Any) -> Any:
    if expected is adt.Type.FLOAT:
        return float(value)
    elif expected in {adt.Type.PATH, adt.Type.SECRET}:
        return str(value)
    else:
        return value


def normalize_value(expected: adt.Type, value: Any) -> Any:
    if expected is adt.Type.FLOAT:
        return float(value)
    elif expected is adt.Type.PATH:
        return api.Path(value) if type(value) is str else value
    elif expected is adt.Type.SECRET:
        return api.Secret(value) if type(value) is str else value
    else:
        return value


def normalize_input(expected: adt.Type, value: Any, coder: Optional[api.Coder]) -> Any:
    if expected is adt.Type.CUSTOM and coder is not None:
        return coder.decode(value)
    else:
        return normalize_value(expected, value)


def now_iso() -> str:
    # Go: time.Now().UTC().Format("2006-01-02T15:04:05.999999-07:00")
    return datetime.now(timezone.utc).isoformat()
