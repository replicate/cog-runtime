import dataclasses
import pathlib
from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Any, Generic, Iterator, List, Optional, Type, TypeVar, Union

########################################
# Custom encoding
########################################

T = TypeVar('T')


class Coder(Generic[T]):
    @abstractmethod
    def encode(self, x: T) -> Any:
        pass

    @abstractmethod
    def decode(self, x: Any) -> T:
        pass


class DataclassCoder(Coder[T], Generic[T]):
    def __init__(self, cls: Type[T]):
        assert dataclasses.is_dataclass(cls)
        self.cls = cls

    def encode(self, x: T) -> dict[str, Any]:
        return dataclasses.asdict(x)  # type: ignore

    def decode(self, x: dict[str, Any]) -> T:
        x = self._from_dict(self.cls, x)
        return self.cls(**x)  # type: ignore

    def _from_dict(self, t, d: dict[str, Any]) -> Any:
        for f in dataclasses.fields(t):
            if dataclasses.is_dataclass(f.type) and f.name in d:
                d[f.name] = f.type(**self._from_dict(f.type, d[f.name]))  # type: ignore
        return d


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
