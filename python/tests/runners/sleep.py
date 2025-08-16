import os
import sys
import time

from cog import BasePredictor, current_scope
from cog.server.exceptions import CancelationException


class Predictor(BasePredictor):
    test_inputs = {'i': 0, 's': 'foo'}

    def setup(self) -> None:
        print('starting setup')
        print('completed setup')

    def predict(self, i: int, s: str) -> str:
        try:
            time.sleep(0.1)
            print('starting prediction')
            if i > 0:
                time.sleep(0.1)
            for x in range(i):
                print(f'prediction in progress {x + 1}/{i}')
                time.sleep(0.1)
            print('completed prediction')
            time.sleep(0.1)
            current_scope().record_metric('i', i)
            current_scope().record_metric('s_len', len(s))
            return f'*{s}*'
        except CancelationException as e:
            print('prediction canceled')
            raise e

class SetupSleepingPredictor(Predictor):
    def setup(self) -> None:
        print('starting setup')

        i = 1
        for x in range(i):
            print(f'setup in progress {x + 1}/{i}')
            time.sleep(0.1)
        print('completed setup')

class SetupFailingPredictor(BasePredictor):
    def setup(self) -> None:
        print('starting setup')
        print('setup failed')
        raise Exception('setup failed')

    def predict(self, i: int, s: str) -> str:
        print('starting prediction')
        return f'*{s}*'

class SetupCrashingPredictor(BasePredictor):
    def setup(self) -> None:
        print('starting setup')
        print('setup crashed')
        sys.exit(1)

    def predict(self, i: int, s: str) -> str:
        print('starting prediction')
        return f'*{s}*'

class PredictionFailingPredictor(BasePredictor):
    def predict(self, i: int, s: str) -> str:
        print('starting prediction')
        print('prediction failed')
        raise Exception('prediction failed')

class PredictionCrashingPredictor(BasePredictor):
    def predict(self, i: int, s: str) -> str:
        print('starting prediction')
        sys.exit(1)