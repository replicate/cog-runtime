import typing
from dataclasses import dataclass
from enum import Enum, auto
from typing import Any, Dict, List, Optional, Union

from coglet import api


class PrimitiveType(Enum):
    BOOL = auto()
    FLOAT = auto()
    INTEGER = auto()
    STRING = auto()
    PATH = auto()
    SECRET = auto()

    @staticmethod
    def _python_type() -> dict:
        return {
            PrimitiveType.BOOL: bool,
            PrimitiveType.FLOAT: float,
            PrimitiveType.INTEGER: int,
            PrimitiveType.STRING: str,
            PrimitiveType.PATH: api.Path,
            PrimitiveType.SECRET: api.Secret,
        }

    @staticmethod
    def _json_type() -> dict:
        return {
            PrimitiveType.BOOL: 'boolean',
            PrimitiveType.FLOAT: 'number',
            PrimitiveType.INTEGER: 'integer',
            PrimitiveType.STRING: 'string',
            PrimitiveType.PATH: 'string',
            PrimitiveType.SECRET: 'string',
        }

    @staticmethod
    def _adt_type() -> dict:
        return {
            bool: PrimitiveType.BOOL,
            float: PrimitiveType.FLOAT,
            int: PrimitiveType.INTEGER,
            str: PrimitiveType.STRING,
            api.Path: PrimitiveType.PATH,
            api.Secret: PrimitiveType.SECRET,
        }

    @staticmethod
    def from_type(tpe: type) -> Optional[Any]:
        return PrimitiveType._adt_type().get(tpe)

    def normalize(self, value: Any) -> Any:
        pt = PrimitiveType._python_type()[self]
        # String-ly types, only upcast
        if self in {self.PATH, self.SECRET}:
            return value if type(value) is pt else pt(value)
        else:
            v = pt(value)
            assert v == value, f'failed to normalize value {value} as {pt}'
            return v

    def json_type(self) -> dict[str, Any]:
        jt: dict[str, Any] = {'type': self._json_type()[self]}
        if self is self.PATH:
            jt['format'] = 'uri'
        elif self is self.SECRET:
            jt['format'] = 'password'
            jt['writeOnly'] = True
            jt['x-cog-secret'] = True
        return jt

    def json_value(self, value: Any) -> Any:
        if self is self.FLOAT:
            return float(value)
        elif self in {self.PATH, self.SECRET}:
            return str(value)
        else:
            return value


class Repetition(Enum):
    REQUIRED = 1
    OPTIONAL = 2
    REPEATED = 3


@dataclass(frozen=True)
class FieldType:
    primitive: PrimitiveType
    repetition: Repetition

    @staticmethod
    def from_type(tpe: type):
        if typing.get_origin(tpe) is list:
            t_args = typing.get_args(tpe)
            assert len(t_args) == 1, 'list must have one type argument'
            elem_t = t_args[0]
            repetition = Repetition.REPEATED
        elif typing.get_origin(tpe) is Union:
            t_args = typing.get_args(tpe)
            assert len(t_args) == 2 and type(None) in t_args, (
                f'unsupported union type {tpe}'
            )
            elem_t = t_args[0] if t_args[1] is type(None) else t_args[0]
            repetition = Repetition.OPTIONAL
        else:
            elem_t = tpe
            repetition = Repetition.REQUIRED
        cog_t = PrimitiveType.from_type(elem_t)
        assert cog_t is not None, f'unsupported Cog type {elem_t}'
        return FieldType(primitive=cog_t, repetition=repetition)

    def normalize(self, value: Any) -> Any:
        if self.repetition is Repetition.REQUIRED:
            return self.primitive.normalize(value)
        elif self.repetition is Repetition.OPTIONAL:
            return None if value is None else self.primitive.normalize(value)
        elif self.repetition is Repetition.REPEATED:
            return [self.primitive.normalize(v) for v in value]

    def json_type(self) -> dict[str, Any]:
        if self.repetition is Repetition.REPEATED:
            return {'type': 'array', 'items': self.primitive.json_type()}
        else:
            return self.primitive.json_type()


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


class Kind(Enum):
    SINGLE = 1
    LIST = 2
    ITERATOR = 3
    CONCAT_ITERATOR = 4
    OBJECT = 5


@dataclass(frozen=True)
class Output:
    kind: Kind
    type: Optional[PrimitiveType] = None
    fields: Optional[Dict[str, FieldType]] = None

    def normalize(self, value: Any) -> Any:
        if self.kind is Kind.SINGLE:
            assert self.type is not None
            return self.type.normalize(value)
        elif self.kind is Kind.LIST:
            assert self.type is not None
            return [self.type.normalize(x) for x in value]
        elif self.kind in {Kind.ITERATOR, Kind.CONCAT_ITERATOR}:
            assert self.type is not None
            return self.type.normalize(value)
        elif self.kind is Kind.OBJECT:
            assert self.fields is not None
            for name, tpe in self.fields.items():
                assert hasattr(value, name), f'missing output field: {name}'
                v = getattr(value, name)
                if v is None:
                    assert tpe.repetition is Repetition.OPTIONAL, (
                        f'missing value for output field: {name}'
                    )
                setattr(value, name, tpe.normalize(v))
            return value

    def json_type(self) -> dict[str, Any]:
        jt: dict[str, Any] = {'title': 'Output'}
        if self.kind is Kind.SINGLE:
            assert self.type is not None
            jt.update(self.type.json_type())
        elif self.kind is Kind.LIST:
            assert self.type is not None
            jt.update({'type': 'array', 'items': self.type.json_type()})
        elif self.kind is Kind.ITERATOR:
            assert self.type is not None
            jt.update(
                {
                    'type': 'array',
                    'items': self.type.json_type(),
                    'x-cog-array-type': 'iterator',
                }
            )
        elif self.kind is Kind.CONCAT_ITERATOR:
            assert self.type is not None
            jt.update(
                {
                    'type': 'array',
                    'items': self.type.json_type(),
                    'x-cog-array-type': 'iterator',
                    'x-cog-array-display': 'concatenate',
                }
            )
        elif self.kind is Kind.OBJECT:
            assert self.fields is not None
            props = {}
            for name, cog_t in self.fields.items():
                props[name] = cog_t.primitive.json_type()
                props[name]['title'] = name.replace('_', ' ').title()
            jt.update(
                {
                    'type': 'object',
                    'properties': props,
                    'required': list(self.fields.keys()),
                }
            )
        return jt


@dataclass(frozen=True)
class Predictor:
    module_name: str
    class_name: str
    inputs: Dict[str, Input]
    output: Output
