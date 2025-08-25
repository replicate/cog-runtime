from cog import BaseModel, BasePredictor


class Item(BaseModel):
    id: int
    name: str


class Predictor(BasePredictor):
    def predict(self) -> list[Item]:
        return [Item(id=1, name='first'), Item(id=2, name='second')]
