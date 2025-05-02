import dataclasses
import inspect
import typing
from dataclasses import dataclass
from enum import Enum, auto
from typing import Any, Callable, Dict, List, Optional, Union

from coglet import api


class PrimitiveType(Enum):
    BOOL = auto()
    FLOAT = auto()
    INTEGER = auto()
    STRING = auto()
    PATH = auto()
    SECRET = auto()
    CUSTOM = auto()

    @staticmethod
    def _python_type() -> dict:
        return {
            PrimitiveType.BOOL: bool,
            PrimitiveType.FLOAT: float,
            PrimitiveType.INTEGER: int,
            PrimitiveType.STRING: str,
            PrimitiveType.PATH: api.Path,
            PrimitiveType.SECRET: api.Secret,
            PrimitiveType.CUSTOM: Any,
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
            PrimitiveType.CUSTOM: 'object',
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
    def from_type(tpe: type) -> Any:
        return PrimitiveType._adt_type().get(tpe, PrimitiveType.CUSTOM)

    def normalize(self, value: Any) -> Any:
        pt = PrimitiveType._python_type()[self]
        if self is PrimitiveType.CUSTOM:
            # Custom type, leave as is
            return value
        elif self in {self.PATH, self.SECRET}:
            # String-ly types, only upcast
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

    def json_encode(self, value: Any) -> Any:
        if self is self.FLOAT:
            return float(value)
        elif self in {self.PATH, self.SECRET}:
            # Leave these as is and let file runner handle special encoding
            return value
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
    coder: Optional[api.Coder]

    @staticmethod
    def from_type(tpe: type):
        if typing.get_origin(tpe) is list:
            t_args = typing.get_args(tpe)
            assert len(t_args) == 1, 'list must have one type argument'
            elem_t = t_args[0]
            # Fail fast to avoid the cryptic "unsupported Cog type" error later with elem_t
            nested_t = typing.get_origin(elem_t)
            assert nested_t is None, f'List cannot have nested type {nested_t}'
            repetition = Repetition.REPEATED
        elif typing.get_origin(tpe) is Union:
            t_args = typing.get_args(tpe)
            assert len(t_args) == 2 and type(None) in t_args, (
                f'unsupported union type {tpe}'
            )
            elem_t = t_args[0] if t_args[1] is type(None) else t_args[0]
            # Fail fast to avoid the cryptic "unsupported Cog type" error later with elem_t
            nested_t = typing.get_origin(elem_t)
            assert nested_t is None, f'Optional cannot have nested type {nested_t}'
            repetition = Repetition.OPTIONAL
        else:
            elem_t = tpe
            repetition = Repetition.REQUIRED
        cog_t = PrimitiveType.from_type(elem_t)
        coder = None
        if cog_t is PrimitiveType.CUSTOM:
            coder = api.Coder.lookup(elem_t)
            assert coder is not None, f'unsupported Cog type {elem_t}'

        return FieldType(primitive=cog_t, repetition=repetition, coder=coder)

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

    def json_encode(self, value: Any) -> Any:
        f: Callable[[Any], Any] = self.primitive.json_encode
        if self.primitive is PrimitiveType.CUSTOM:
            assert self.coder is not None
            f = self.coder.encode
        if self.repetition is Repetition.REPEATED:
            return [f(x) for x in value]
        else:
            return f(value)

    def json_decode(self, value: Any) -> Any:
        if self.primitive is not PrimitiveType.CUSTOM:
            return value
        assert self.coder is not None
        f = self.coder.decode
        if self.repetition is Repetition.REPEATED:
            return [f(x) for x in value]
        else:
            return f(value)


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
    coder: Optional[api.Coder] = None

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

    def _transform(self, value: Any, json: bool) -> Any:
        if self.kind in {Kind.SINGLE, Kind.ITERATOR, Kind.CONCAT_ITERATOR}:
            assert self.type is not None
            f = self.type.json_encode if json else self.type.normalize
            return f(value)
        elif self.kind is Kind.LIST:
            assert self.type is not None
            f = self.type.json_encode if json else self.type.normalize
            return [f(x) for x in value]
        elif self.kind is Kind.OBJECT:
            assert self.fields is not None
            for name, ft in self.fields.items():
                f = ft.json_encode if json else ft.normalize
                assert hasattr(value, name), f'missing output field: {name} {value}'
                v = getattr(value, name)
                if v is None:
                    assert ft.repetition is Repetition.OPTIONAL, (
                        f'missing value for output field: {name}'
                    )
                setattr(value, name, f(v))
            return value
        raise RuntimeError(f'unsupported output kind {self.kind}')

    def normalize(self, value: Any) -> Any:
        return self._transform(value, json=False)

    def json_encode(self, value: Any) -> Any:
        if self.coder is not None:
            if self.kind is Kind.LIST:
                return [self.coder.encode(x) for x in value]
            else:
                return self.coder.encode(value)
        o = self._transform(value, json=True)
        if self.kind is Kind.OBJECT:
            # Further expand Output into dict
            tpe = type(o)
            assert tpe.__name__ == 'Output' and any(
                c is api.BaseModel for c in inspect.getmro(tpe)
            )
            r = {}
            for f in dataclasses.fields(o):
                r[f.name] = getattr(o, f.name)
            return r
        else:
            return o


@dataclass(frozen=True)
class Predictor:
    module_name: str
    class_name: str
    inputs: Dict[str, Input]
    output: Output
