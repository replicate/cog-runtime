from typing import Any, TypedDict

from cog import BasePredictor
from cog.coder import json_coder  # noqa: F401


class InputTypedDict(TypedDict):
    message: str


class Predictor(BasePredictor):
    test_inputs = {'json': InputTypedDict(message='foo')}

    def predict(self, json: InputTypedDict) -> dict[str, Any]:
        msg = json.get('message')
        if msg is not None:
            json['message'] = f'*{msg}*'
        return json
