from cog import BaseModel, BasePredictor


class Output(BaseModel):
    x: list[int]
    y: str
    z: list[str]


FIXTURE = [
    ({}, Output(x=[1, 2, 3], y='hello', z=['a', 'b'])),
]


class Predictor(BasePredictor):
    setup_done = False

    def setup(self) -> None:
        self.setup_done = True

    def predict(self) -> Output:
        return Output(x=[1, 2, 3], y='hello', z=['a', 'b'])
