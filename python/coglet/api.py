import pathlib
import sys
from abc import ABC, abstractmethod
from dataclasses import dataclass, is_dataclass
from typing import Any, AsyncIterator, Iterator, List, Optional, Type, TypeVar, Union

########################################
# Custom encoding
########################################


# Encoding between a custom type and JSON dict[str, Any]
class Coder:
    _coders: set = set()

    @staticmethod
    def register(coder) -> None:
        Coder._coders.add(coder)

    @staticmethod
    def lookup(tpe: Type) -> Optional[Any]:
        for cls in Coder._coders:
            c = cls.factory(tpe)
            if c is not None:
                return c
        return None

    @staticmethod
    @abstractmethod
    def factory(cls: Type) -> Optional[Any]:
        pass

    @abstractmethod
    def encode(self, x: Any) -> dict[str, Any]:
        pass

    @abstractmethod
    def decode(self, x: dict[str, Any]) -> Any:
        pass


########################################
# Data types
########################################


class CancelationException(Exception):
    pass


class Path(pathlib.PosixPath):
    pass


@dataclass(frozen=True)
class Secret:
    secret_value: Optional[str] = None

    def __repr__(self):
        return f'Secret({str(self)})'

    def __str__(self):
        return '**********' if self.secret_value is not None else ''

    def get_secret_value(self) -> Optional[str]:
        return self.secret_value


_T_co = TypeVar('_T_co', covariant=True)


class ConcatenateIterator(Iterator[_T_co]):
    @abstractmethod
    def __next__(self) -> _T_co: ...


class AsyncConcatenateIterator(AsyncIterator[_T_co]):
    @abstractmethod
    async def __anext__(self) -> _T_co: ...


########################################
# Input, Output
########################################


@dataclass(frozen=True)
class Input:
    default: Any = None
    description: Optional[str] = None
    ge: Optional[Union[int, float]] = None
    le: Optional[Union[int, float]] = None
    min_length: Optional[int] = None
    max_length: Optional[int] = None
    regex: Optional[str] = None
    choices: Optional[List[Union[str, int]]] = None
    deprecated: Optional[bool] = None


# pyodide does not recognise `BaseModel` with the `__new__` keyword as a dataclass while regular python does.
# to get around this, we hijack `__init__subclass` instead to make sure the subclass of a base model is recognised
# as a dataclass. In addition to this, we provide this `BaseModel` only on pyodide instances, normal python still gets
# the regular `BaseModel``.
class _BaseModelPyodide:
    def __init_subclass__(cls, **kwargs):
        dc_keys = {
            'init',
            'repr',
            'eq',
            'order',
            'unsafe_hash',
            'frozen',
            'match_args',
            'kw_only',
            'slots',
            'weakref_slot',
        }
        dc_opts = {k: kwargs.pop(k) for k in list(kwargs) if k in dc_keys}
        super().__init_subclass__(**kwargs)
        if not is_dataclass(cls):
            dataclass(**dc_opts)(cls)


class _BaseModelStd:
    def __new__(cls, *args, **kwargs):
        # This does not work with frozen=True
        # Also user might want to mutate the output class
        dcls = dataclass()(cls)
        return super().__new__(dcls)


BaseModel = _BaseModelPyodide if 'pyodide' in sys.modules else _BaseModelStd


########################################
# Predict
########################################


class BasePredictor(ABC):
    def setup(
        self,
        weights: Optional[Union[Path, str]] = None,
    ) -> None:
        return

    @abstractmethod
    def predict(self, **kwargs: Any) -> Any:
        return NotImplemented


########################################
# Logging
########################################


# Compat: for current_scope warning
# https://github.com/replicate/cog/blob/main/python/cog/types.py#L41
class ExperimentalFeatureWarning(Warning):
    pass
