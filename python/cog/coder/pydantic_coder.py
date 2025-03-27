import inspect
from typing import Any, Type

from pydantic import BaseModel

from coglet import api


class BaseModelCoder(api.Coder):
    @staticmethod
    def factory(cls: Type):
        if cls is not BaseModel and any(c is BaseModel for c in inspect.getmro(cls)):
            return BaseModelCoder(cls)
        else:
            return None

    def __init__(self, cls: Type[BaseModel]):
        self.cls = cls

    def encode(self, x: BaseModel) -> dict[str, Any]:
        return x.model_dump(exclude_unset=True)

    def decode(self, x: dict[str, Any]) -> BaseModel:
        return self.cls.model_construct(**x)
