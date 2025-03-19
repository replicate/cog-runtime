import typing
from datetime import datetime, timezone
from typing import Any, Union

from coglet import adt, api


def get_field_type(tpe: type) -> adt.FieldType:
    if typing.get_origin(tpe) is list:
        t_args = typing.get_args(tpe)
        assert len(t_args) == 1, 'list must have one type argument'
        elem_t = t_args[0]
        repetition = adt.Repetition.REPEATED
    elif typing.get_origin(tpe) is Union:
        t_args = typing.get_args(tpe)
        assert len(t_args) == 2 and type(None) in t_args, (
            f'unsupported union type {tpe}'
        )
        elem_t = t_args[0] if t_args[1] is type(None) else t_args[0]
        repetition = adt.Repetition.OPTIONAL
    else:
        elem_t = tpe
        repetition = adt.Repetition.REQUIRED
    cog_t = adt.PYTHON_TO_COG.get(elem_t)
    assert cog_t is not None, f'unsupported Cog type {elem_t}'
    return adt.FieldType(primitive=cog_t, repetition=repetition)


def check_value(expected: adt.PrimitiveType, value: Any) -> bool:
    cog_t = adt.PYTHON_TO_COG.get(type(value))
    if cog_t is None:
        return False
    elif expected is adt.PrimitiveType.FLOAT:
        return cog_t in adt.NUMERIC_TYPES
    elif expected is adt.PrimitiveType.PATH:
        return cog_t in adt.PATH_TYPES
    elif expected is adt.PrimitiveType.SECRET:
        return cog_t in adt.SECRET_TYPES
    else:
        return cog_t is expected


def json_value(expected: adt.PrimitiveType, value: Any) -> Any:
    if expected is adt.PrimitiveType.FLOAT:
        return float(value)
    elif expected in {adt.PrimitiveType.PATH, adt.PrimitiveType.SECRET}:
        return str(value)
    else:
        return value


def normalize_value(expected: adt.PrimitiveType, value: Any) -> Any:
    if expected is adt.PrimitiveType.FLOAT:
        return float(value)
    elif expected is adt.PrimitiveType.PATH:
        return api.Path(value) if type(value) is str else value
    elif expected is adt.PrimitiveType.SECRET:
        return api.Secret(value) if type(value) is str else value
    else:
        return value


def now_iso() -> str:
    # Go: time.Now().UTC().Format("2006-01-02T15:04:05.999999-07:00")
    return datetime.now(timezone.utc).isoformat()
