import inspect
from typing import Any

from coglet import api

# TODO: check overflow
NUMPY_INTS = {
    'int8',
    'int16',
    'int32',
    'int64',
    'uint8',
    'uint16',
    'uint32',
    'uint64',
    'intp',
    'uintp',
}
NUMPY_FLOATS = {
    'float16',
    'float32',
    'float64',
    'float96',
    'float128',
}


def normalize_value(value: Any) -> Any:
    t = type(value)
    if inspect.isclass(t) and hasattr(t, '__module__') and t.__module__ == 'numpy':
        if t.__name__ in NUMPY_INTS:
            return int(value)
        elif t.__name__ in NUMPY_FLOATS:
            return float(value)
        else:
            return value
    else:
        return value


def normalize(value: Any) -> Any:
    t = type(value)
    if (
        inspect.isclass(t)
        and hasattr(t, '__module__')
        and t.__module__ == 'numpy'
        and t.__name__ == 'ndarray'
    ):
        return [normalize_value(v) for v in value]
    elif type(value) is list:
        return [normalize_value(v) for v in value]
    else:
        return normalize_value(value)


# Normalize value types we don't officially support
def normalize_input(cog_in: api.Input) -> api.Input:
    return api.Input(
        default=normalize(cog_in.default),
        description=cog_in.description,
        ge=normalize(cog_in.ge),
        le=normalize(cog_in.le),
        min_length=normalize(cog_in.min_length),
        max_length=normalize(cog_in.max_length),
        regex=cog_in.regex,
        choices=normalize(cog_in.choices),
    )
