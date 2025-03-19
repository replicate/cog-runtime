import typing
from dataclasses import dataclass
from enum import Enum
from typing import Any, Dict, Iterator, List, Optional, Union

from coglet import api


class PrimitiveType(Enum):
    BOOL = 1
    FLOAT = 2
    INTEGER = 3
    STRING = 4
    PATH = 5
    SECRET = 6


class Repetition(Enum):
    REQUIRED = 1
    OPTIONAL = 2
    REPEATED = 3


@dataclass(frozen=True)
class FieldType:
    primitive: PrimitiveType
    repetition: Repetition


NUMERIC_TYPES = {PrimitiveType.FLOAT, PrimitiveType.INTEGER}
PATH_TYPES = {PrimitiveType.STRING, PrimitiveType.PATH}
SECRET_TYPES = {PrimitiveType.STRING, PrimitiveType.SECRET}
CHOICE_TYPES = {PrimitiveType.INTEGER, PrimitiveType.STRING}


class Kind(Enum):
    SINGLE = 1
    LIST = 2
    ITERATOR = 3
    CONCAT_ITERATOR = 4
    OBJECT = 5


ARRAY_KINDS = {
    Kind.LIST,
    Kind.ITERATOR,
    Kind.CONCAT_ITERATOR,
}

# Python types to Cog types
PYTHON_TO_COG = {
    bool: PrimitiveType.BOOL,
    float: PrimitiveType.FLOAT,
    int: PrimitiveType.INTEGER,
    str: PrimitiveType.STRING,
    api.Path: PrimitiveType.PATH,
    api.Secret: PrimitiveType.SECRET,
}

# Cog types to JSON types
COG_TO_JSON = {
    PrimitiveType.BOOL: 'boolean',
    PrimitiveType.FLOAT: 'number',
    PrimitiveType.INTEGER: 'integer',
    PrimitiveType.STRING: 'string',
    PrimitiveType.PATH: 'string',
    PrimitiveType.SECRET: 'string',
}

# JSON types to Cog types
# PATH and SECRET depend on format field
JSON_TO_COG = {
    'boolean': PrimitiveType.BOOL,
    'number': PrimitiveType.FLOAT,
    'integer': PrimitiveType.INTEGER,
    'string': PrimitiveType.STRING,
}

# Python container types to Cog types
CONTAINER_TO_COG = {
    list: Kind.LIST,
    typing.get_origin(Iterator): Kind.ITERATOR,
    api.ConcatenateIterator: Kind.CONCAT_ITERATOR,
}


@dataclass(frozen=True)
class Input:
    name: str
    order: int
    type: FieldType
    default: Any = None
    description: Optional[str] = None
    ge: Optional[Union[int, float]] = None
    le: Optional[Union[int, float]] = None
    min_length: Optional[int] = None
    max_length: Optional[int] = None
    regex: Optional[str] = None
    choices: Optional[List[Union[str, int]]] = None


@dataclass(frozen=True)
class Output:
    kind: Kind
    type: Optional[PrimitiveType] = None
    fields: Optional[Dict[str, FieldType]] = None


@dataclass(frozen=True)
class Predictor:
    module_name: str
    class_name: str
    inputs: Dict[str, Input]
    output: Output
