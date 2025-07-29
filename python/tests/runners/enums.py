from enum import Enum

from cog import BasePredictor, Input


class Colors(Enum):
    RED = 'red'
    GREEN = 'green'
    BLUE = 'blue'


class Numbers(Enum):
    E = 2.71828
    PHI = 1.618
    PI = 3.14


class Predictor(BasePredictor):
    test_inputs = {
        'c': Colors.GREEN,
        'n': Numbers.PI,
    }

    def predict(
        self, c: str = Input(default=Colors.RED), n: float = Input(default=Numbers.E)
    ) -> str:
        return c
