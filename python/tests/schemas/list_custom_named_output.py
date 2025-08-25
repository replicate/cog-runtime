from cog import BaseModel, BasePredictor


class Item(BaseModel):
    id: int
    name: str


FIXTURE = [
    ({}, [Item(id=1, name='first'), Item(id=2, name='second')]),
]


class Predictor(BasePredictor):
    setup_done = False

    def setup(self) -> None:
        self.setup_done = True

    def predict(self) -> list[Item]:
        return [Item(id=1, name='first'), Item(id=2, name='second')]
