import time
from typing import Iterator

from cog import BasePredictor


class Predictor(BasePredictor):
    def predict(self, i: int, s: str) -> Iterator[str]:
        print('starting prediction')
        for x in range(i):
            print(f'prediction in progress {x+1}/{i}')
            time.sleep(1)
            yield f'*{s}-{x}*'
        print('completed prediction')
