import pathlib
from abc import ABC, abstractmethod
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from typing import Any, Iterator, List, Optional, TypeVar, Union, Callable, Generator

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


class BaseModel:
    def __new__(cls, *args, **kwargs):
        # This does not work with frozen=True
        # Also user might want to mutate the output class
        dcls = dataclass()(cls)
        return super().__new__(dcls)


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
# Scope
########################################


class Scope:
    _record_metric: Callable[[str, Union[float, int]], None]
    _tag: Optional[str] = None

    def __init__(self, record_metric: Callable[[str, Union[float, int]], None], tag: Optional[str] = None):
        self._record_metric = record_metric
        self._tag = tag

    @property
    def record_metric(self) -> Callable[[str, Union[float, int]], None]:
        return self._record_metric

    def copy(self, **kwargs: Any) -> 'Scope':
        return Scope(record_metric=self.record_metric, **kwargs)


_current_scope: ContextVar[Optional[Scope]] = ContextVar("scope", default=None)


def current_scope() -> Scope:
    return _get_current_scope()


def _get_current_scope() -> Scope:
    s = _current_scope.get()
    if s is None:
        raise RuntimeError("No scope available")
    return s


@contextmanager
def scope(sc: Scope) -> Generator[None, None, None]:
    s = _current_scope.set(sc)
    try:
        yield
    finally:
        _current_scope.reset(s)


@contextmanager
def evolve_scope(**kwargs: Any) -> Generator[None, None, None]:
    new_scope = _get_current_scope().copy(**kwargs)
    s = _current_scope.set(new_scope)
    try:
        yield
    finally:
        _current_scope.reset(s)
