from typing import List

from cog import BaseModel, BasePredictor

# Updated: list fields in BaseModel are now supported
# Change this test to test nested lists which should still fail
ERROR = 'List cannot have nested type list'


class Output(BaseModel):
    xs: List[List[str]]  # Nested lists should still be invalid


class Predictor(BasePredictor):
    def setup(self) -> None:
        pass

    def predict(self, s: str) -> Output:
        return Output(xs=[])
