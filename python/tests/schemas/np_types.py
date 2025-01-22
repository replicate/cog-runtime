from typing import List

import numpy as np

from cog import BasePredictor, Input

FIXTURE = [
    ({}, '3.14,0,1,foo,[2.71,3.14],[3,4]'),
]


class Predictor(BasePredictor):
    setup_done = False

    def setup(self) -> None:
        self.setup_done = True

    def predict(
        self,
        f: float = Input(
            default=np.float64(3.14), ge=np.float64(0.0), le=np.float64(10.0)
        ),
        i: int = Input(default=np.int64(0), ge=np.int64(0), le=np.int64(10)),
        # Compat: Cog fails when choices=np.int64([0, 1, 2])
        c: int = Input(default=np.int64(1), choices=list(np.int64([0, 1, 2]))),
        s: str = Input(default='foo', min_length=np.int64(0), max_length=np.int64(10)),
        l1: List[float] = Input(default=np.float64([2.71, 3.14])),
        l2: List[int] = Input(default=np.int64([3, 4])),
    ) -> str:
        def f2s(xs):
            return ','.join(f'{x:.2f}' for x in xs)

        def i2s(xs):
            return ','.join(f'{x}' for x in xs)

        return f'{f:.2f},{i},{c},{s},[{f2s(l1)}],[{i2s(l2)}]'
