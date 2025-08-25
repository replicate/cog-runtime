from cog import BaseModel, BasePredictor


class CustomOutput(BaseModel):
    value: int
    message: str


class Predictor(BasePredictor):
    def predict(self) -> CustomOutput:
        return CustomOutput(value=42, message='hello world')
