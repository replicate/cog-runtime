from cog import BaseModel, BasePredictor


class CustomOut(BaseModel):
    x: int
    y: str


class Predictor(BasePredictor):
    test_inputs = {'i': 3}

    def predict(self, i: int) -> list[CustomOut]:
        outputs: list[CustomOut] = []
        while i > 0:
            outputs.append(CustomOut(x=i, y='a'))
            i -= 1
        return outputs
