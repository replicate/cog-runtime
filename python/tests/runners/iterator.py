import time
from typing import Iterator

from cog import BasePredictor


class Predictor(BasePredictor):
    def predict(self, i: int, s: str) -> Iterator[str]:
        for x in range(i):
            time.sleep(1)
            yield f'*{s}-{x}*'
