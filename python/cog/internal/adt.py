import typing
from dataclasses import dataclass
from enum import Enum
from typing import Any, Dict, Iterator, List, Optional, Union

import cog


class Type(Enum):
    BOOL = 1
    FLOAT = 2
    INTEGER = 3
    STRING = 4
    PATH = 5
    SECRET = 6


NUMERIC_TYPES = {Type.FLOAT, Type.INTEGER}
PATH_TYPES = {Type.STRING, Type.PATH}
SECRET_TYPES = {Type.STRING, Type.SECRET}
CHOICE_TYPES = {Type.INTEGER, Type.STRING}


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
    bool: Type.BOOL,
    float: Type.FLOAT,
    int: Type.INTEGER,
    str: Type.STRING,
    cog.Path: Type.PATH,
    cog.Secret: Type.SECRET,
}

# Cog types to JSON types
COG_TO_JSON = {
    Type.BOOL: 'boolean',
    Type.FLOAT: 'number',
    Type.INTEGER: 'integer',
    Type.STRING: 'string',
    Type.PATH: 'string',
    Type.SECRET: 'string',
}

# JSON types to Cog types
# PATH and SECRET depend on format field
JSON_TO_COG = {
    'boolean': Type.BOOL,
    'number': Type.FLOAT,
    'integer': Type.INTEGER,
    'string': Type.STRING,
}

# Python container types to Cog types
CONTAINER_TO_COG = {
    list: Kind.LIST,
    typing.get_origin(Iterator): Kind.ITERATOR,
    cog.ConcatenateIterator: Kind.CONCAT_ITERATOR,
}


@dataclass(frozen=True)
class Input:
    name: str
    order: int
    type: Type
    is_list: bool
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
    type: Optional[Type] = None
    fields: Optional[Dict[str, Type]] = None


@dataclass(frozen=True)
class Predictor:
    module_name: str
    class_name: str
    inputs: Dict[str, Input]
    output: Output
