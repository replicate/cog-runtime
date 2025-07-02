from cog import BaseModel, BasePredictor

ERROR = 'output type must be named Output: BadOutput'


class BadOutput(BaseModel):
    pass


class Predictor(BasePredictor):
    def setup(self) -> None:
        pass

    def predict(self, s: str) -> BadOutput:
        return BadOutput()
