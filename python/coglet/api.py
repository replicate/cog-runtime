import pathlib
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


class BaseModel:
    def __init_subclass__(
        cls, *, auto_dataclass: bool = True, init: bool = True, **kwargs
    ):
        # BaseModel is parented to `object` so we have nothing to pass up to it, we pass the kwargs to dataclass() only.
        super().__init_subclass__()

        # For sanity, the primary base class must inherit from BaseModel
        if not issubclass(cls.__bases__[0], BaseModel):
            raise TypeError(
                f'Primary base class of "{cls.__name__}" must inherit from BaseModel'
            )
        elif not auto_dataclass:
            try:
                if (
                    cls.__bases__[0] != BaseModel
                    and cls.__bases__[0].__auto_dataclass is True  # type: ignore[attr-defined]
                ):
                    raise ValueError(
                        f'Primary base class of "{cls.__name__}" ("{cls.__bases__[0].__name__}") has auto_dataclass=True, but "{cls.__name__}" has auto_dataclass=False. This creates broken field inheritance.'
                    )
            except AttributeError:
                raise RuntimeError(
                    f'Primary base class of "{cls.__name__}" is a child of a child of `BaseModel`, but `auto_dataclass` tracking does not exist. This is likely a bug or other programming error.'
                )

        for base in cls.__bases__[1:]:
            if is_dataclass(base):
                raise TypeError(
                    f'Cannot mixin dataclass "{base.__name__}" while inheriting from `BaseModel`'
                )

        # Once manual dataclass handling is enabled, we never apply the auto dataclass logic again,
        # it becomes the responsibility of the user to ensure that all dataclass semantics are handled.
        if not auto_dataclass:
            cls.__auto_dataclass = False  # type: ignore[attr-defined]
            return

        # all children should be dataclass'd, this is the only way to ensure that the dataclass inheritence
        # is handled properly.
        dataclass(init=init, **kwargs)(cls)
        cls.__auto_dataclass = True  # type: ignore[attr-defined]


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
