import pathlib
from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Any, Iterator, List, Optional, TypeVar, Union

########################################
# Data types
########################################


class CancelationException(Exception):
    pass


class Path(pathlib.PosixPath):
    def __init__(self, *args):
        super().__init__()
        self.is_empty = args == () or args == ('',)


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
