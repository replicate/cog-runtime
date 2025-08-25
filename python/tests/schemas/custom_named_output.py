from cog import BaseModel, BasePredictor


class CustomOutput(BaseModel):
    value: int
    message: str


FIXTURE = [
    ({}, CustomOutput(value=42, message='hello world')),
]


class Predictor(BasePredictor):
    setup_done = False

    def setup(self) -> None:
        self.setup_done = True

    def predict(self) -> CustomOutput:
        return CustomOutput(value=42, message='hello world')
