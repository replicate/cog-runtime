import typing
from typing import Any, Optional, Type

from coglet import api


class JsonCoder(api.Coder):
    @staticmethod
    def factory(cls: Type) -> Optional[api.Coder]:
        if typing.get_origin(cls) is dict is dict and typing.get_args(cls)[0] is str:
            return JsonCoder()
        else:
            return None

    def encode(self, x: Any) -> dict[str, Any]:
        return x

    def decode(self, x: dict[str, Any]) -> Any:
        return x
