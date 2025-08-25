from cog import BaseModel, BasePredictor


class Output(BaseModel):
    x: list[int]
    y: str


class Predictor(BasePredictor):
    def predict(self) -> Output:
        return Output(x=[1, 2, 3], y="hello")
