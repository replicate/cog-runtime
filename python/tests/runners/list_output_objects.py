from cog import BaseModel, BasePredictor


class Output(BaseModel):
    x: int


class Predictor(BasePredictor):
    def predict(self) -> list[Output]:
        return [Output(x=1), Output(x=2), Output(x=3)]
