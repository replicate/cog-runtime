from cog import BasePredictor, Input, Path

FIXTURE = [
    ({}, 'None,None,None,None,None'),
    (
        {
            'b': True,
            'f': 3.14,
            'i': 1,
            's': 'bar',
            'p': Path('bar.txt'),
        },
        'True,3.14,1,bar,bar.txt',
    ),
]


class Predictor(BasePredictor):
    setup_done = False

    def setup(self) -> None:
        self.setup_done = True

    def predict(
        self,
        b: bool = Input(default=None),
        f: float = Input(default=None),
        i: int = Input(default=None),
        s: str = Input(default=None),
        p: Path = Input(default=None),
    ) -> str:
        fs = f'{f:.2f}' if f is not None else 'None'
        return f'{b},{fs},{i},{s},{p}'
