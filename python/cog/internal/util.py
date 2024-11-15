import typing
from datetime import datetime, timezone
from typing import Any, Tuple

import cog
from cog.internal import adt


def check_cog_type(tpe: type) -> Tuple[adt.Type, bool]:
    if typing.get_origin(tpe) is list:
        t_args = typing.get_args(tpe)
        assert len(t_args) == 1, 'list must have one type argument'
        elem_t = t_args[0]
        is_list = True
    else:
        elem_t = tpe
        is_list = False
    cog_t = adt.PYTHON_TO_COG.get(elem_t)
    assert cog_t is not None, f'unsupported Cog type {elem_t}'
    return cog_t, is_list


def check_value(expected: adt.Type, value: Any) -> bool:
    cog_t = adt.PYTHON_TO_COG.get(type(value))
    if cog_t is None:
        return False
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
        return cog.Path(value) if type(value) is str else value
    elif expected is adt.Type.SECRET:
        return cog.Secret(value) if type(value) is str else value
    else:
        return value


def now_iso() -> str:
    # Go: time.Now().UTC().Format("2006-01-02T15:04:05.999999-07:00")
    return datetime.now(timezone.utc).isoformat()
