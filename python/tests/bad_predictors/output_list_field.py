from typing import List

from cog import BaseModel, BasePredictor

ERROR = 'output field must not be list: xs: List[str]'


class Output(BaseModel):
    xs: List[str]


class Predictor(BasePredictor):
    def setup(self) -> None:
        pass

    def predict(self, s: str) -> Output:
        return Output([])
